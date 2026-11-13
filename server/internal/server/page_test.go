package server

import (
	"log/slog"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/thde/wetpaint/server/internal/game"
	"github.com/thde/wetpaint/server/internal/paint"
	"github.com/thde/wetpaint/server/internal/protocol"
	"github.com/thde/wetpaint/server/internal/store"
)

// These tests drive a Page by hand, one tick at a time, with connections
// that are nothing but their outbound queue. The tick counter is the only
// clock a page has, so minutes of game time pass in milliseconds.

func newTestPage(t *testing.T) *Page {
	t.Helper()
	hub := NewHub(Config{}, store.NewMemory(time.Now), slog.New(slog.DiscardHandler))
	return newPage(1, hub, game.NewWorld(rand.New(rand.NewPCG(1, 2)), game.Options{}))
}

// testClient is a seated connection without a socket.
type testClient struct {
	t      *testing.T
	page   *Page
	c      *client
	closed []websocket.StatusCode // close codes the page asked for
}

// seat connects a new spectator the way handleWS does.
func seat(t *testing.T, p *Page) *testClient {
	t.Helper()
	if !p.seats.reserve() {
		t.Fatal("page has no room")
	}
	tc := &testClient{t: t, page: p, c: newClient(nil, func() {})}
	tc.c.close = func(code websocket.StatusCode, _ string) { tc.closed = append(tc.closed, code) }
	p.handle(command{kind: cmdConnect, client: tc.c})
	return tc
}

func (tc *testClient) send(m protocol.ClientMessage) {
	tc.page.handle(command{kind: cmdMessage, client: tc.c, msg: m})
}

func (tc *testClient) disconnect() {
	tc.page.handle(command{kind: cmdDisconnect, client: tc.c})
}

// drain returns everything queued for the client, in order, decoded.
func (tc *testClient) drain() []protocol.ServerMessage {
	tc.t.Helper()
	var msgs []protocol.ServerMessage
	for {
		out, ok := tc.c.queue.pop()
		if !ok {
			return msgs
		}
		data := out.data
		if out.build != nil {
			data = out.build()
		}
		m, err := protocol.DecodeServer(data)
		if err != nil {
			tc.t.Fatalf("undecodable message of type %#x: %v", data[0], err)
		}
		msgs = append(msgs, m)
	}
}

// mirror is a client-side paint grid kept in sync from the wire alone.
type mirror struct {
	t      *testing.T
	grid   *paint.Grid
	ops    int
	checks int
}

func loadGrid(t *testing.T, tick uint32, w, h uint16, z []byte) *paint.Grid {
	t.Helper()
	planes, err := paint.Decompress(z, int(w)*int(h)*paint.SnapshotBytesPerCell)
	if err != nil {
		t.Fatal(err)
	}
	g := paint.NewGrid(int(w), int(h), game.Ticks(game.FadeSeconds), 0)
	if err := g.LoadSnapshot(planes, tick); err != nil {
		t.Fatal(err)
	}
	return g
}

func (m *mirror) apply(msgs []protocol.ServerMessage) {
	m.t.Helper()
	for _, msg := range msgs {
		switch msg := msg.(type) {
		case *protocol.Welcome:
			m.grid = loadGrid(m.t, msg.Tick, msg.GridW, msg.GridH, msg.Grid)
		case *protocol.Grid:
			m.grid = loadGrid(m.t, msg.Tick, msg.GridW, msg.GridH, msg.Data)
		case *protocol.Events:
			for _, op := range msg.Paint {
				m.grid.Apply(op, msg.Tick)
				m.ops++
			}
		case *protocol.GridCheck:
			m.checks++
			if got := m.grid.Checksum(msg.Tick); got != msg.CRC {
				m.t.Fatalf("tick %d: client grid has checksum %#08x, server says %#08x", msg.Tick, got, msg.CRC)
			}
		}
	}
}

func TestGridChecksMatchAGridBuiltFromTheWire(t *testing.T) {
	t.Parallel()
	p := newTestPage(t)
	early := seat(t, p)
	early.send(protocol.Join{Seed: 1, Color: 1})
	painter := &mirror{t: t}
	var late *testClient
	watcher := &mirror{t: t} // joins once the page is painted and partly faded

	checkTicks := game.Ticks(gridCheckInterval.Seconds())
	const seconds = 80 // long enough for the drop to die and bloom
	for tick := range uint32(seconds * game.TickRate) {
		// Steer in a slow circle; ask for a new life whenever one ends.
		dx, dy := int8(127), int8(0)
		if tick/60%2 == 1 {
			dx, dy = 0, 127
		}
		early.send(protocol.Input{Seq: tick, DX: dx, DY: dy})
		early.send(protocol.Respawn{Color: protocol.ColorRandom})
		if tick == 40*game.TickRate {
			late = seat(t, p)
		}
		p.tick()
		painter.apply(early.drain())
		if late != nil {
			watcher.apply(late.drain())
		}
	}

	if want := int(seconds * game.TickRate / checkTicks); painter.checks != want {
		t.Errorf("first client verified %d grid checks, want %d", painter.checks, want)
	}
	if painter.ops < 500 {
		t.Errorf("only %d paint ops were sent; the test did not paint", painter.ops)
	}
	if watcher.checks < 7 {
		t.Errorf("late joiner verified %d grid checks, want at least 7", watcher.checks)
	}
	if got, want := watcher.grid.Checksum(p.world.Tick()), painter.grid.Checksum(p.world.Tick()); got != want {
		t.Errorf("late joiner's grid differs from the first client's: %#08x vs %#08x", got, want)
	}
}

func TestResyncSendsAFreshGridAtMostEveryTenSeconds(t *testing.T) {
	t.Parallel()
	p := newTestPage(t)
	c := seat(t, p)
	c.send(protocol.Join{Seed: 1, Color: 4})
	m := &mirror{t: t}
	grids := func(msgs []protocol.ServerMessage) (n int) {
		for _, msg := range msgs {
			if _, ok := msg.(*protocol.Grid); ok {
				n++
			}
		}
		return n
	}
	step := func(ticks uint32) {
		for i := range ticks {
			c.send(protocol.Input{Seq: i, DX: 90, DY: 90})
			c.send(protocol.Respawn{Color: protocol.ColorRandom})
			p.tick()
			m.apply(c.drain())
		}
	}
	step(3 * game.TickRate)

	// Simulate a client whose grid has gone wrong.
	m.grid.Apply(paint.Op{X: 1000, Y: 1000, Radius2: 200, R: 1, G: 2, B: 3, Strength: 255}, p.world.Tick())
	if m.grid.Checksum(p.world.Tick()) == p.world.Grid().Checksum(p.world.Tick()) {
		t.Fatal("corrupting the mirror did not change its checksum")
	}

	c.send(protocol.Resync{})
	c.send(protocol.Resync{}) // ignored: too soon
	msgs := c.drain()
	if n := grids(msgs); n != 1 {
		t.Fatalf("two Resyncs in a row produced %d grids, want 1", n)
	}
	m.apply(msgs)
	if got, want := m.grid.Checksum(p.world.Tick()), p.world.Grid().Checksum(p.world.Tick()); got != want {
		t.Errorf("after Grid the mirror has checksum %#08x, server %#08x", got, want)
	}

	// Paint ops after the Grid apply on top of it, and later checks pass.
	cooldown := game.Ticks(resyncCooldown.Seconds())
	step(cooldown - 1)
	c.send(protocol.Resync{})
	if n := grids(c.drain()); n != 0 {
		t.Errorf("Resync %d ticks after the last one was honoured", cooldown-1)
	}
	step(1)
	c.send(protocol.Resync{})
	if n := grids(c.drain()); n != 1 {
		t.Errorf("Resync after the cooldown produced %d grids, want 1", n)
	}
	if m.checks < 2 {
		t.Errorf("only %d grid checks verified", m.checks)
	}
}

func TestPageSeatsPlayersAndSpectatorsSeparately(t *testing.T) {
	t.Parallel()
	p := newTestPage(t)
	a, b := seat(t, p), seat(t, p)
	if got, want := seated(p), (seatCount{spectators: 2}); got != want {
		t.Fatalf("seats = %+v, want %+v", got, want)
	}
	setSeats(p, game.PageCap-1, 2) // a crowd arrives: one player seat is left
	if !p.seats.hasRoom() {
		t.Error("page with a free player seat and free spectator seats has no room")
	}

	a.send(protocol.Join{Seed: 1, Color: 0})
	a.send(protocol.Join{Seed: 1, Color: 0}) // a second Join takes no second seat
	if got, want := seated(p), (seatCount{players: game.PageCap, spectators: 1}); got != want {
		t.Errorf("seats after the last player joined = %+v, want %+v", got, want)
	}
	if p.seats.hasRoom() || p.seats.reserve() {
		t.Error("full page still takes connections")
	}

	b.send(protocol.Join{Seed: 1, Color: 0})
	if len(b.closed) != 1 || b.closed[0] != protocol.CloseFull {
		t.Errorf("Join on a full page: close codes %v, want [%d]", b.closed, protocol.CloseFull)
	}
	if b.c.player.Alive {
		t.Error("the refused player spawned anyway")
	}
	b.send(protocol.Join{Seed: 1, Color: 0}) // ignored while closing
	if len(b.closed) != 1 {
		t.Errorf("closed %d times", len(b.closed))
	}
	b.disconnect()
	if got, want := seated(p), (seatCount{players: game.PageCap}); got != want {
		t.Errorf("seats after the refused spectator left = %+v, want %+v", got, want)
	}

	// A dead player keeps the seat; leaving frees it.
	p.world.RemovePlayer(a.c.player) // kills the drop
	if got := seated(p).players; got != game.PageCap {
		t.Errorf("a dead player's seat was freed: %d players", got)
	}
	a.disconnect()
	if got, want := seated(p), (seatCount{players: game.PageCap - 1}); got != want {
		t.Errorf("seats after a player left = %+v, want %+v", got, want)
	}
	if !p.seats.hasRoom() {
		t.Error("page has no room after a player left")
	}
}

func TestIdleConnectionsAreClosed(t *testing.T) {
	t.Parallel()
	p := newTestPage(t)
	idle := game.Ticks(game.IdleSeconds)
	spectator, player := seat(t, p), seat(t, p)
	player.send(protocol.Join{Seed: 1, Color: 0})

	// tick advances the page; the player respawns at once while keen.
	keen := true
	tick := func() {
		if keen {
			player.send(protocol.Respawn{Color: protocol.ColorRandom})
		}
		p.tick()
		spectator.drain()
		player.drain()
	}

	for range idle - 1 {
		tick()
	}
	if len(spectator.closed) != 0 {
		t.Fatalf("spectator closed after %d ticks, before the %d-tick limit", idle-1, idle)
	}
	tick()
	if len(spectator.closed) != 1 || spectator.closed[0] != protocol.CloseIdle {
		t.Fatalf("idle spectator: close codes %v, want [%d]", spectator.closed, protocol.CloseIdle)
	}
	tick()
	if len(spectator.closed) != 1 {
		t.Errorf("spectator closed %d times", len(spectator.closed))
	}

	// The player has died several times by now but never stayed dead.
	if len(player.closed) != 0 {
		t.Fatalf("active player was closed: %v", player.closed)
	}

	// Once the player stops coming back, the clock starts at the death.
	keen = false
	var deadFor uint32
	for range 2*idle + game.Ticks(120) {
		tick()
		if player.c.player.Alive {
			continue
		}
		deadFor++
		if closed := len(player.closed) == 1; closed != (deadFor >= idle) {
			t.Fatalf("dead for %d ticks (limit %d): closed = %v", deadFor, idle, closed)
		}
		if deadFor > idle {
			break
		}
	}
	if len(player.closed) != 1 || player.closed[0] != protocol.CloseIdle {
		t.Errorf("idle dead player: close codes %v, want [%d]", player.closed, protocol.CloseIdle)
	}
}

// BenchmarkTickFullPage measures a whole page tick (simulation, encoding and
// queueing) for a full page whose writers keep up.
func BenchmarkTickFullPage(b *testing.B) {
	hub := NewHub(Config{}, store.NewMemory(time.Now), slog.New(slog.DiscardHandler))
	p := newPage(1, hub, game.NewWorld(rand.New(rand.NewPCG(1, 2)), game.Options{}))
	done := make(chan struct{})
	defer close(done)
	var clients []*client
	for i := range game.PageCap {
		p.seats.reserve()
		c := newClient(nil, func() {})
		c.close = func(websocket.StatusCode, string) {}
		p.handle(command{kind: cmdConnect, client: c})
		p.handle(command{kind: cmdMessage, client: c, msg: protocol.Join{Seed: 1, Color: uint8(i % 10)}})
		clients = append(clients, c)
		go func() { // stands in for writeLoop
			for {
				if _, ok := c.queue.pop(); ok {
					continue
				}
				select {
				case <-c.queue.wake:
				case <-done:
					return
				}
			}
		}()
	}
	var seq uint32
	for b.Loop() {
		seq++
		for i, c := range clients {
			dx := int8(127)
			if (int(seq)/40+i)%2 == 0 {
				dx = -127
			}
			p.handle(command{kind: cmdMessage, client: c, msg: protocol.Input{Seq: seq, DX: dx, DY: int8(i - 30)}})
			p.handle(command{kind: cmdMessage, client: c, msg: protocol.Respawn{Color: protocol.ColorRandom}})
		}
		p.tick()
	}
}
