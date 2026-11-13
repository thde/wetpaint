package server

import (
	"cmp"
	"context"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/coder/websocket"

	"github.com/thde/wetpaint/server/internal/game"
	"github.com/thde/wetpaint/server/internal/names"
	"github.com/thde/wetpaint/server/internal/paint"
	"github.com/thde/wetpaint/server/internal/protocol"
	"github.com/thde/wetpaint/server/internal/store"
)

const (
	inboxSize         = 1024
	snapshotInterval  = 30 * time.Second
	boardSize         = 5
	leaderboardPeriod = game.TickRate // ticks; 1 Hz
	gridCheckInterval = 5 * time.Second
	resyncCooldown    = 10 * time.Second
)

type commandKind uint8

const (
	cmdConnect commandKind = iota
	cmdMessage
	cmdDisconnect
)

// command is something a connection wants from the page goroutine.
type command struct {
	kind   commandKind
	client *client
	msg    protocol.ClientMessage
}

// Page runs one sheet of paper. Its goroutine (run) owns the world and the
// client set exclusively; everything else talks to it through channels and
// the seat counts.
type Page struct {
	id     uint32
	hub    *Hub
	log    *slog.Logger
	inbox  chan command
	topsIn chan store.Tops
	done   chan struct{}
	seats  seats

	// Owned by run.
	world      *game.World
	clients    map[uint16]*client
	tops       store.Tops
	emptyTicks uint32
	state      protocol.State // scratch
	live       []*game.Player // scratch
}

func newPage(id uint32, hub *Hub, world *game.World) *Page {
	return &Page{
		id:      id,
		hub:     hub,
		log:     hub.log.With("page", id),
		inbox:   make(chan command, inboxSize),
		topsIn:  make(chan store.Tops, 1),
		done:    make(chan struct{}),
		world:   world,
		clients: make(map[uint16]*client),
	}
}

// send delivers a command to the page goroutine, giving up when ctx ends or
// the page has stopped.
func (p *Page) send(ctx context.Context, cmd command) bool {
	select {
	case p.inbox <- cmd:
		return true
	case <-ctx.Done():
		return false
	case <-p.done:
		return false
	}
}

// offerTops hands the page fresh leaderboards, replacing any it has not
// picked up yet.
func (p *Page) offerTops(t store.Tops) {
	select {
	case <-p.topsIn:
	default:
	}
	select {
	case p.topsIn <- t:
	default:
	}
}

// run is the page's tick loop. It returns when ctx ends or the page retires.
func (p *Page) run(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(time.Second / game.TickRate)
	defer ticker.Stop()
	m := p.hub.metrics
	// Hub.retire drops these series; a page stopped by shutdown keeps them.
	playersGauge, spectatorsGauge := m.pageGauges(p.id)

	for {
		select {
		case <-ctx.Done():
			p.saveSnapshot()
			return
		case cmd := <-p.inbox:
			p.handle(cmd)
		case p.tops = <-p.topsIn:
		case <-ticker.C:
			start := time.Now()
			p.tick()
			m.tickDuration.UpdateDuration(start)
			players, spectators := p.seats.count()
			playersGauge.Set(float64(players))
			spectatorsGauge.Set(float64(spectators))
			if p.idleTooLong() {
				p.log.Info("page retired")
				return
			}
		}
	}
}

// idleTooLong retires a page that has been empty long enough for its paint
// to have faded.
func (p *Page) idleTooLong() bool {
	if len(p.clients) > 0 {
		p.emptyTicks = 0
		return false
	}
	p.emptyTicks++
	return p.emptyTicks > game.Ticks(game.FadeSeconds) && p.hub.retire(p)
}

func (p *Page) handle(cmd command) {
	c := cmd.client
	switch cmd.kind {
	case cmdConnect:
		p.connect(c)
	case cmdDisconnect:
		p.seats.leave(c.joined)
		if c.player != nil {
			p.world.RemovePlayer(c.player)
			delete(p.clients, c.player.ID)
			c.player = nil
		}
	case cmdMessage:
		if c.player == nil || c.kicked {
			return
		}
		switch m := cmd.msg.(type) {
		case protocol.Join:
			if !c.joined {
				if !p.seats.join() {
					p.close(c, protocol.CloseFull, "page full")
					return
				}
				c.joined = true
			}
			p.world.Join(c.player, names.Sanitize(m.Name), m.Color)
		case protocol.Respawn:
			p.world.Respawn(c.player, m.Color)
		case protocol.Input:
			p.world.SetInput(c.player, m.Seq, m.DX, m.DY)
		case protocol.View:
			p.world.SetView(c.player, float64(m.W), float64(m.H))
		case protocol.Ping:
			pong := protocol.Pong{ClientTime: m.ClientTime, Tick: p.world.Tick()}
			p.droppable(c, pong.Append(nil))
		case protocol.Resync:
			p.resync(c)
		}
	}
}

// connect seats a new connection and queues its Welcome. The grid planes
// are copied here; the writer goroutine compresses them.
func (p *Page) connect(c *client) {
	player := p.world.AddPlayer()
	if player == nil {
		c.cancel()
		return
	}
	c.player = player
	c.idleSince = p.world.Tick()
	p.clients[player.ID] = c

	welcome := &protocol.Welcome{Version: protocol.Version, Page: p.id, YourID: player.ID}
	p.world.FillWelcome(welcome)
	planes := p.world.Grid().AppendSnapshot(nil, p.world.Tick())
	p.reliable(c, outbound{build: func() []byte {
		welcome.Grid = paint.Compress(planes)
		return welcome.Append(nil)
	}})
}

// resync answers a client whose grid no longer matches with a fresh one. A
// client may ask once per resyncCooldown; anything more is ignored.
func (p *Page) resync(c *client) {
	tick := p.world.Tick()
	if c.resynced && tick-c.resyncAt < game.Ticks(resyncCooldown.Seconds()) {
		return
	}
	c.resynced, c.resyncAt = true, tick
	p.hub.metrics.resyncs.Inc()

	w, h := p.world.Grid().Size()
	grid := &protocol.Grid{Tick: tick, GridW: uint16(w), GridH: uint16(h)} //nolint:gosec // grid dimensions are small constants
	planes := p.world.Grid().AppendSnapshot(nil, tick)
	p.reliable(c, outbound{build: func() []byte {
		grid.Data = paint.Compress(planes)
		return grid.Append(nil)
	}})
}

// reliable queues a message that must not be lost. A client too far behind
// to take it is disconnected: its paint grid would no longer match.
func (p *Page) reliable(c *client, msg outbound) {
	if c.kicked {
		return
	}
	if ok, _ := c.queue.push(msg); !ok {
		c.kicked = true
		c.cancel()
		p.hub.metrics.clientsKicked.Inc()
		p.log.Info("kicked slow client", "player", c.player.ID)
	}
}

// droppable queues a message that a newer one makes worthless.
func (p *Page) droppable(c *client, data []byte) {
	if c.kicked {
		return
	}
	if _, dropped := c.queue.push(outbound{data: data, droppable: true}); dropped {
		p.hub.metrics.statesDropped.Inc()
	}
}

// close ends c's connection with a close code that tells the client what to
// do next.
func (p *Page) close(c *client, code websocket.StatusCode, reason string) {
	if c.kicked {
		return
	}
	c.kicked = true
	c.close(code, reason)
}

func (p *Page) tick() {
	out := p.world.Step()

	p.broadcastEvents(out)
	for _, d := range out.Deaths {
		if c := p.clients[d.Player.ID]; c != nil {
			p.reliable(c, outbound{data: d.Dead.Append(nil)})
		}
		p.recordScores(d)
	}

	// After this tick's Events, so the checksum covers exactly the paint
	// ops every client has been sent.
	if out.Tick%game.Ticks(gridCheckInterval.Seconds()) == 0 && len(p.clients) > 0 {
		check := protocol.GridCheck{Tick: out.Tick, CRC: p.world.Grid().Checksum(out.Tick)}
		p.broadcastDroppable(check.Append(nil))
	}

	idleTicks := game.Ticks(game.IdleSeconds)
	for _, c := range p.clients {
		switch {
		case c.kicked:
			continue
		case c.player.Alive:
			c.idleSince = out.Tick
		case out.Tick-c.idleSince >= idleTicks:
			p.close(c, protocol.CloseIdle, "idle")
			continue
		}
		p.world.FillState(&p.state, c.player)
		p.droppable(c, p.state.Append(make([]byte, 0, 64+16*len(p.state.Drops))))
	}

	if out.Tick%leaderboardPeriod == 0 && len(p.clients) > 0 {
		p.broadcastDroppable(p.leaderboard().Append(nil))
	}
	if out.Tick%game.Ticks(snapshotInterval.Seconds()) == 0 {
		p.saveSnapshot()
	}
}

// broadcastEvents sends the tick's paint ops and events to everyone, split
// into several messages in the unlikely case one cannot hold them.
func (p *Page) broadcastEvents(out game.Output) {
	paintOps, events := out.Paint, out.Events
	for len(paintOps) > 0 || len(events) > 0 {
		msg := protocol.Events{Tick: out.Tick}
		n := min(len(paintOps), protocol.MaxPaintOps)
		msg.Paint, paintOps = paintOps[:n], paintOps[n:]
		n = min(len(events), protocol.MaxEvents)
		msg.Events, events = events[:n], events[n:]

		data := msg.Append(make([]byte, 0, 8+9*len(msg.Paint)+16*len(msg.Events)))
		for _, c := range p.clients {
			p.reliable(c, outbound{data: data})
		}
	}
}

func (p *Page) broadcastDroppable(data []byte) {
	for _, c := range p.clients {
		p.droppable(c, data)
	}
}

// recordScores queues a finished life for the persistent leaderboards.
func (p *Page) recordScores(d game.DeathReport) {
	values := [store.NumBoards]uint32{
		store.Painted: d.Dead.CellsPainted,
		store.Longest: uint32(d.Dead.SecondsAlive),
		store.Mixed:   uint32(d.Dead.ColorsMixed),
	}
	page, name, color, at := p.id, d.Name, d.Color, time.Now()
	p.hub.persist.submit(job{what: "record scores", run: func(ctx context.Context, s store.Store) error {
		for board, value := range values {
			if value == 0 {
				continue
			}
			entry := protocol.Entry{Name: name, Value: value, Color: color}
			if err := s.RecordScore(ctx, page, store.Board(board), entry, at); err != nil {
				return err
			}
		}
		return nil
	}})
}

// leaderboard combines the live top five with the stored boards.
func (p *Page) leaderboard() *protocol.Leaderboard {
	p.live = p.live[:0]
	for _, pl := range p.world.Players() {
		if pl.Alive {
			p.live = append(p.live, pl)
		}
	}
	lb := &protocol.Leaderboard{}
	lb.Boards[protocol.BoardLivePainted] = p.liveBoard(func(pl *game.Player) uint32 { return pl.CellsPainted })
	lb.Boards[protocol.BoardLiveLongest] = p.liveBoard(func(pl *game.Player) uint32 { return uint32(p.world.SecondsAlive(pl)) })
	lb.Boards[protocol.BoardLiveMixed] = p.liveBoard(func(pl *game.Player) uint32 { return uint32(pl.ColorsMixed) })
	for b := range int(store.NumBoards) {
		lb.Boards[protocol.BoardHourPainted+b] = p.tops.Hour[b]
		lb.Boards[protocol.BoardGlobalPainted+b] = p.tops.Global[b]
	}
	return lb
}

func (p *Page) liveBoard(value func(*game.Player) uint32) []protocol.Entry {
	slices.SortStableFunc(p.live, func(a, b *game.Player) int { return cmp.Compare(value(b), value(a)) })
	entries := make([]protocol.Entry, 0, boardSize)
	for _, pl := range p.live[:min(len(p.live), boardSize)] {
		if v := value(pl); v > 0 {
			entries = append(entries, protocol.Entry{Name: pl.Name, Value: v, Color: pl.Color.RGB()})
		}
	}
	return entries
}

// saveSnapshot copies the grid and queues it for compression and storage.
func (p *Page) saveSnapshot() {
	page, tick := p.id, p.world.Tick()
	planes := p.world.Grid().AppendSnapshot(nil, tick)
	p.hub.persist.submit(job{what: "save snapshot", run: func(ctx context.Context, s store.Store) error {
		return s.SaveSnapshot(ctx, page, encodeSnapshot(tick, planes))
	}})
}

// restoreWorld builds a page's world, resuming from a stored snapshot when
// there is a usable one.
func restoreWorld(newRand func() *rand.Rand, snapshot []byte, log *slog.Logger) *game.World {
	if len(snapshot) > 0 {
		tick, planes, err := decodeSnapshot(snapshot)
		if err == nil && tick < math.MaxUint32/2 {
			world := game.NewWorld(newRand(), game.Options{StartTick: tick})
			if err = world.Grid().LoadSnapshot(planes, tick); err == nil {
				log.Info("restored paint grid", "tick", tick)
				return world
			}
		}
		log.Warn("ignoring unusable snapshot", "err", err)
	}
	return game.NewWorld(newRand(), game.Options{})
}
