package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/coder/websocket"

	"github.com/thde/wetpaint/server/internal/protocol"
)

// Indices into Welcome.Constants that bots care about; see the protocol
// document for the full list.
const (
	constWorldW           = 0
	constWorldH           = 1
	constTickRate         = 2
	constRadiusBase       = 7
	constRadiusPerWetness = 8
	constAbsorbRatio      = 19
	numConstsNeeded       = 20
)

const (
	thirstyBelow  = 65.0  // wetness at which a bot goes looking for water
	chaseRange    = 450.0 // how far a bot bothers to chase or flee
	arriveRadius  = 60.0  // throttle eases off inside this distance
	respawnDelay  = time.Second
	reconnectWait = 2 * time.Second
	readLimit     = 4 << 20 // a Welcome with a busy grid is a few hundred kB
)

type point struct{ x, y float64 }

func (p point) dist(q point) float64 { return math.Hypot(p.x-q.x, p.y-q.y) }

type bot struct {
	name  string // for logs; the server picks the name players see
	seed  uint32 // keeps the bot's name across reconnects
	url   string
	lazy  bool
	rng   *rand.Rand
	stats *stats
	log   *slog.Logger

	// Per-connection state, reset by play.
	consts   []float32
	droplets map[uint16]point
	state    *protocol.State
	seq      uint32
	wander   point
	wanderAt time.Time
	hunting  bool
	respawn  time.Time // when to ask for a new life; zero while alive
}

// runForever keeps the bot connected until ctx ends.
func (b *bot) runForever(ctx context.Context) {
	for ctx.Err() == nil {
		err := b.play(ctx)
		if ctx.Err() != nil {
			return
		}
		b.stats.reconnects.Add(1)
		b.log.Debug("bot disconnected", "bot", b.name, "err", err)
		if websocket.CloseStatus(err) == protocol.CloseFull {
			// The page filled up between Welcome and Join; the server
			// places the next connection on a page with room.
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(reconnectWait):
		}
	}
}

// play runs one connection: join, then steer at the server's tick rate.
func (b *bot) play(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, b.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)
	b.stats.connected.Add(1)
	defer b.stats.connected.Add(-1)

	msg, err := read(ctx, conn)
	if err != nil {
		return err
	}
	welcome, ok := msg.(*protocol.Welcome)
	if !ok || len(welcome.Constants) < numConstsNeeded {
		return fmt.Errorf("%w: expected a v%d Welcome, got %T", errProtocol, protocol.Version, msg)
	}
	b.consts = welcome.Constants
	b.droplets = make(map[uint16]point, len(welcome.Droplets))
	for _, d := range welcome.Droplets {
		b.droplets[d.ID] = point{d.X.Units(), d.Y.Units()}
	}
	b.state, b.seq, b.respawn = nil, 0, time.Time{}

	send := func(m protocol.ClientMessage) error {
		return conn.Write(ctx, websocket.MessageBinary, m.Append(nil))
	}
	if err := send(protocol.Join{Seed: b.seed, Color: protocol.ColorRandom}); err != nil {
		return fmt.Errorf("join: %w", err)
	}

	// The reader owns the socket's read side and forwards decoded messages.
	incoming := make(chan protocol.ServerMessage, 64)
	readErr := make(chan error, 1)
	go func() {
		for {
			m, err := read(ctx, conn)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case incoming <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	alive := false
	defer func() {
		if alive {
			b.stats.alive.Add(-1)
		}
	}()
	ticker := time.NewTicker(time.Second / time.Duration(b.consts[constTickRate]))
	defer ticker.Stop()
	for tick := 0; ; {
		select {
		case <-ctx.Done():
			if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
				return fmt.Errorf("close: %w", err)
			}
			return nil
		case err := <-readErr:
			return err
		case m := <-incoming:
			switch m := m.(type) {
			case *protocol.State:
				b.state = m
				switch {
				case m.Alive && !alive:
					b.stats.alive.Add(1)
				case !m.Alive && alive:
					b.stats.alive.Add(-1)
				}
				alive = m.Alive
			case *protocol.Events:
				b.track(m)
			case *protocol.Dead:
				b.stats.deaths.Add(1)
				b.stats.lifeMillis.Add(int64(m.SecondsAlive * 1000))
				b.respawn = time.Now().Add(respawnDelay)
			}
		case now := <-ticker.C:
			tick++
			if tick%int(b.consts[constTickRate]) == 0 {
				if err := send(protocol.Ping{ClientTime: uint32(tick)}); err != nil {
					return fmt.Errorf("ping: %w", err)
				}
			}
			if !b.respawn.IsZero() && now.After(b.respawn) {
				b.respawn = time.Time{}
				if err := send(protocol.Respawn{Color: protocol.ColorRandom}); err != nil {
					return fmt.Errorf("respawn: %w", err)
				}
			}
			if b.state == nil || !b.state.Alive {
				continue
			}
			b.seq++
			dx, dy := b.steer(now)
			if err := send(protocol.Input{Seq: b.seq, DX: dx, DY: dy}); err != nil {
				return fmt.Errorf("input: %w", err)
			}
		}
	}
}

func read(ctx context.Context, conn *websocket.Conn) (protocol.ServerMessage, error) {
	_, frame, err := conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	m, err := protocol.DecodeServer(frame)
	if err != nil {
		return nil, fmt.Errorf("decode %d-byte frame of type %#x: %w", len(frame), frame[0], err)
	}
	return m, nil
}

// track keeps the bot's picture of the droplets on the page current.
func (b *bot) track(ev *protocol.Events) {
	for _, e := range ev.Events {
		switch e := e.(type) {
		case protocol.DropletSpawn:
			b.droplets[e.ID] = point{e.X.Units(), e.Y.Units()}
		case protocol.DropletGone:
			delete(b.droplets, e.ID)
		}
	}
}

func (b *bot) radius(wetness float64) float64 {
	return float64(b.consts[constRadiusBase]) + wetness*float64(b.consts[constRadiusPerWetness])
}

// steer decides where to go and returns the input vector to get there.
func (b *bot) steer(now time.Time) (dx, dy int8) {
	s := b.state
	me := point{float64(s.Self.X), float64(s.Self.Y)}
	target := b.target(now, me)

	d := me.dist(target)
	if d < 1 {
		return 0, 0
	}
	throttle := min(1, d/arriveRadius)
	return int8(math.Round((target.x - me.x) / d * throttle * 127)),
		int8(math.Round((target.y - me.y) / d * throttle * 127))
}

func (b *bot) target(now time.Time, me point) point {
	s := b.state
	myRadius := b.radius(float64(s.Self.Wetness))
	ratio := float64(b.consts[constAbsorbRatio])

	// Run from anything close that could swallow us.
	for _, d := range s.Drops {
		other := point{d.X.Units(), d.Y.Units()}
		if b.radius(float64(d.Wetness2)/2) > ratio*myRadius && me.dist(other) < chaseRange/2 {
			return b.clamp(point{me.x + (me.x - other.x), me.y + (me.y - other.y)})
		}
	}

	if !b.lazy && s.Self.Wetness < thirstyBelow {
		return b.nearestWater(me)
	}

	// Every so often pick a new spot to amble to and decide whether this
	// stretch is a hunting one.
	if now.After(b.wanderAt) || me.dist(b.wander) < arriveRadius {
		b.wander = point{
			b.rng.Float64() * float64(b.consts[constWorldW]),
			b.rng.Float64() * float64(b.consts[constWorldH]),
		}
		b.wanderAt = now.Add(time.Duration(3+b.rng.IntN(5)) * time.Second)
		b.hunting = b.rng.IntN(3) == 0
	}
	if b.hunting {
		best, bestDist := b.wander, chaseRange
		for _, d := range s.Drops {
			other := point{d.X.Units(), d.Y.Units()}
			if dist := me.dist(other); dist < bestDist {
				best, bestDist = other, dist
			}
		}
		return best
	}
	return b.wander
}

// nearestWater returns the closest droplet or puddle, or the spot beneath
// the cloud if the page has neither.
func (b *bot) nearestWater(me point) point {
	s := b.state
	best := point{s.CloudX.Units(), s.CloudY.Units() + 100}
	bestDist := math.Inf(1)
	consider := func(p point) {
		if d := me.dist(p); d < bestDist {
			best, bestDist = p, d
		}
	}
	for _, p := range b.droplets {
		consider(p)
	}
	for _, p := range s.Puddles {
		consider(point{p.X.Units(), p.Y.Units()})
	}
	return best
}

func (b *bot) clamp(p point) point {
	return point{
		min(max(p.x, 0), float64(b.consts[constWorldW])),
		min(max(p.y, 0), float64(b.consts[constWorldH])),
	}
}
