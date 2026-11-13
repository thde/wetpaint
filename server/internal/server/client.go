package server

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/thde/wetpaint/server/internal/game"
	"github.com/thde/wetpaint/server/internal/protocol"
)

const (
	// idleTimeout closes a connection that sends nothing for this long.
	idleTimeout = 3 * time.Second
	// maxMessagesPerSecond closes a connection that sends more than this.
	maxMessagesPerSecond = 60
	// maxFrameBytes bounds a client frame; the largest is a Join.
	maxFrameBytes = 512
	writeTimeout  = 5 * time.Second

	// reliableQueue is how many reliable messages (mostly Events) a client
	// may fall behind, about six seconds of ticks, before it is disconnected.
	reliableQueue = 128
	// droppableQueue is deliberately tiny: stale states are worthless. It
	// holds what one tick can produce (State, Leaderboard, Pong, GridCheck).
	droppableQueue = 4
)

// outbound is one queued message: either encoded bytes shared between
// clients, or a builder that the writer goroutine runs so that expensive
// encoding (compressing a grid snapshot) stays off the page loop.
type outbound struct {
	data      []byte
	build     func() []byte
	droppable bool // may be discarded in favour of a newer droppable message
}

// sendQueue is a client's outbound queue. The page goroutine pushes and the
// writer goroutine pops. It is a single queue, rather than one per delivery
// class, because the protocol promises that messages arrive in the order the
// page produced them: a GridCheck is only meaningful if it follows exactly
// the Events it describes.
type sendQueue struct {
	mu        sync.Mutex
	items     []outbound
	droppable int           // droppable messages among items
	wake      chan struct{} // signalled when items becomes non-empty
}

func newSendQueue() *sendQueue { return &sendQueue{wake: make(chan struct{}, 1)} }

// push appends msg without blocking. A droppable message pushes out the
// oldest queued droppable one when the client is not keeping up, leaving the
// order of everything else intact; dropped reports that. A reliable message
// is refused (ok is false) once the client is too far behind.
func (q *sendQueue) push(msg outbound) (ok, dropped bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case !msg.droppable:
		if len(q.items)-q.droppable >= reliableQueue {
			return false, false
		}
	case q.droppable >= droppableQueue:
		i := slices.IndexFunc(q.items, func(m outbound) bool { return m.droppable })
		q.items = slices.Delete(q.items, i, i+1)
		dropped = true
	default:
		q.droppable++
	}
	q.items = append(q.items, msg)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return true, dropped
}

// pop removes the oldest message, if there is one.
func (q *sendQueue) pop() (outbound, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return outbound{}, false
	}
	msg := q.items[0]
	q.items[0] = outbound{} // let the payload go
	q.items = q.items[1:]
	if msg.droppable {
		q.droppable--
	}
	return msg, true
}

// client is one WebSocket connection.
type client struct {
	conn   *websocket.Conn
	queue  *sendQueue
	cancel context.CancelFunc // tears the connection down from any goroutine
	// close ends the connection with a close code the client acts on. It
	// does not block.
	close func(code websocket.StatusCode, reason string)

	// Owned by the page goroutine.
	player    *game.Player
	joined    bool   // holds a player seat rather than a spectator seat
	kicked    bool   // being disconnected; nothing more is sent or accepted
	idleSince uint32 // tick the drop was last alive, or of the connection
	resyncAt  uint32 // tick of the last Resync honoured
	resynced  bool
}

func newClient(conn *websocket.Conn, cancel context.CancelFunc) *client {
	return &client{
		conn:   conn,
		queue:  newSendQueue(),
		cancel: cancel,
		close: func(code websocket.StatusCode, reason string) {
			// Close waits for the peer's reply, which the page loop must not.
			go func() {
				_ = conn.Close(code, reason)
				cancel()
			}()
		},
	}
}

// writeLoop drains the queue onto the socket until ctx ends or a write
// fails.
func (c *client) writeLoop(ctx context.Context, m *hubMetrics) {
	defer c.cancel()
	for {
		msg, ok := c.queue.pop()
		if !ok {
			select {
			case <-c.queue.wake:
				continue
			case <-ctx.Done():
				return
			}
		}
		data := msg.data
		if msg.build != nil {
			data = msg.build()
		}
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		err := c.conn.Write(wctx, websocket.MessageBinary, data)
		cancel()
		if err != nil {
			return
		}
		m.bytesSent.Add(len(data))
	}
}

// readLoop decodes frames and forwards them to the page until the client
// goes quiet, misbehaves or disconnects. It returns the close status and
// reason to send, if the server is the one ending the connection.
func (c *client) readLoop(ctx context.Context, p *Page) (status websocket.StatusCode, reason string) {
	var (
		windowStart time.Time
		inWindow    int
	)
	for {
		rctx, cancel := context.WithTimeout(ctx, idleTimeout)
		typ, frame, err := c.conn.Read(rctx)
		cancel()
		if err != nil {
			return websocket.StatusGoingAway, "bye"
		}

		if now := time.Now(); now.Sub(windowStart) >= time.Second {
			windowStart, inWindow = now, 0
		}
		inWindow++
		if inWindow > maxMessagesPerSecond {
			return websocket.StatusPolicyViolation, "too many messages"
		}

		if typ != websocket.MessageBinary {
			return websocket.StatusUnsupportedData, "binary frames only"
		}
		msg, err := protocol.DecodeClient(frame)
		if err != nil {
			return websocket.StatusProtocolError, "malformed message"
		}
		if !p.send(ctx, command{kind: cmdMessage, client: c, msg: msg}) {
			return websocket.StatusGoingAway, "bye"
		}
	}
}
