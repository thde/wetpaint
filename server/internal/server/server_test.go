package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/thde/wetpaint/server/internal/game"
	"github.com/thde/wetpaint/server/internal/paint"
	"github.com/thde/wetpaint/server/internal/protocol"
	"github.com/thde/wetpaint/server/internal/store"
)

// testServer is a running hub behind an httptest server.
type testServer struct {
	t     *testing.T
	hub   *Hub
	store store.Store
	http  *httptest.Server
	stop  func() // shuts the hub down and waits; idempotent
}

func startServer(t *testing.T, s store.Store) *testServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	hub := NewHub(Config{}, s, slog.New(slog.DiscardHandler))
	if err := hub.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	srv := httptest.NewServer(hub.Handler())
	ts := &testServer{t: t, hub: hub, store: s, http: srv}
	stopped := false
	ts.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		srv.Close()
		hub.Wait()
	}
	t.Cleanup(ts.stop)
	return ts
}

func (ts *testServer) dial(query string) *websocket.Conn {
	ts.t.Helper()
	url := "ws" + strings.TrimPrefix(ts.http.URL, "http") + "/ws" + query
	conn, _, err := websocket.Dial(context.Background(), url, nil) //nolint:bodyclose // websocket.Dial's response body never needs closing
	if err != nil {
		ts.t.Fatal(err)
	}
	conn.SetReadLimit(4 << 20)
	ts.t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func (ts *testServer) get(path string) string {
	ts.t.Helper()
	req, err := http.NewRequestWithContext(ts.t.Context(), http.MethodGet, ts.http.URL+path, http.NoBody)
	if err != nil {
		ts.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		ts.t.Fatalf("GET %s: status %d, err %v", path, resp.StatusCode, err)
	}
	return string(body)
}

func send(t *testing.T, conn *websocket.Conn, m protocol.ClientMessage) {
	t.Helper()
	if err := conn.Write(context.Background(), websocket.MessageBinary, m.Append(nil)); err != nil {
		t.Fatal(err)
	}
}

// readUntil reads messages until match accepts one, failing after timeout.
func readUntil[T protocol.ServerMessage](t *testing.T, conn *websocket.Conn, match func(T) bool) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, frame, err := conn.Read(ctx)
		if err != nil {
			var zero T
			t.Fatalf("waiting for %T: %v", zero, err)
		}
		m, err := protocol.DecodeServer(frame)
		if err != nil {
			t.Fatalf("undecodable frame of type %#x: %v", frame[0], err)
		}
		if typed, ok := m.(T); ok && match(typed) {
			return typed
		}
	}
}

func anyMessage[T protocol.ServerMessage](T) bool { return true }

func TestConnectJoinAndPlay(t *testing.T) {
	t.Parallel()
	ts := startServer(t, store.NewMemory(time.Now))
	conn := ts.dial("")

	// Welcome arrives first, unprompted, with a grid that inflates.
	_, frame, err := conn.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, err := protocol.DecodeServer(frame)
	if err != nil {
		t.Fatal(err)
	}
	welcome, ok := m.(*protocol.Welcome)
	if !ok {
		t.Fatalf("first message is %T, want Welcome", m)
	}
	if welcome.Version != protocol.Version || welcome.Page != 1 || welcome.YourID == 0 {
		t.Errorf("welcome header = %+v", welcome)
	}
	if len(welcome.Constants) != 20 || len(welcome.Palette) != 10 || len(welcome.Blotters) < game.BlottersMin {
		t.Errorf("welcome: %d constants, %d colours, %d blotters", len(welcome.Constants), len(welcome.Palette), len(welcome.Blotters))
	}
	size := int(welcome.GridW) * int(welcome.GridH) * paint.SnapshotBytesPerCell
	if _, err := paint.Decompress(welcome.Grid, size); err != nil || welcome.GridW != 400 || welcome.GridH != 300 {
		t.Errorf("grid %d×%d: %v", welcome.GridW, welcome.GridH, err)
	}

	// Spectators get states too, marked not alive.
	if st := readUntil(t, conn, anyMessage[*protocol.State]); st.Alive {
		t.Error("spectator state says alive")
	}

	send(t, conn, protocol.Join{Name: "<b>Pip</b>", Color: 2})
	readUntil(t, conn, func(ev *protocol.Events) bool {
		for _, e := range ev.Events {
			if s, ok := e.(protocol.Spawn); ok && s.ID == welcome.YourID {
				if s.Name != "Pip" {
					t.Errorf("spawned as %q, want sanitised %q", s.Name, "Pip")
				}
				return true
			}
		}
		return false
	})
	first := readUntil(t, conn, func(st *protocol.State) bool { return st.Alive })
	if first.Self.Wetness < 99 || first.Self.Color != welcome.Palette[2] {
		t.Errorf("fresh drop: %+v", first.Self)
	}

	// Steering moves the drop, acknowledges the input and paints.
	painted := false
	for seq := uint32(1); seq <= 20; seq++ {
		send(t, conn, protocol.Input{Seq: seq, DX: 127, DY: 0})
		time.Sleep(20 * time.Millisecond)
	}
	moved := readUntil(t, conn, func(st *protocol.State) bool { return st.AckSeq >= 10 })
	if !(moved.Self.X > first.Self.X) || moved.Self.VX <= 0 {
		t.Errorf("drop did not move right: x %v → %v, vx %v", first.Self.X, moved.Self.X, moved.Self.VX)
	}
	send(t, conn, protocol.Input{Seq: 21, DX: 127, DY: 0})
	readUntil(t, conn, func(ev *protocol.Events) bool {
		painted = painted || len(ev.Paint) > 0
		return painted
	})

	send(t, conn, protocol.Ping{ClientTime: 4242})
	if pong := readUntil(t, conn, anyMessage[*protocol.Pong]); pong.ClientTime != 4242 || pong.Tick == 0 {
		t.Errorf("pong = %+v", pong)
	}

	lb := readUntil(t, conn, func(lb *protocol.Leaderboard) bool { return len(lb.Boards[protocol.BoardLivePainted]) > 0 })
	if e := lb.Boards[protocol.BoardLivePainted][0]; e.Name != "Pip" || e.Value == 0 {
		t.Errorf("live board = %+v", lb.Boards[protocol.BoardLivePainted])
	}

	if body := ts.get("/metrics"); !strings.Contains(body, `wetpaint_players{page="1"} 1`) ||
		!strings.Contains(body, "wetpaint_tick_duration_seconds_count") ||
		!strings.Contains(body, "wetpaint_bytes_sent_total") {
		t.Errorf("metrics missing expected series:\n%s", grepLines(body, "wetpaint_"))
	}
	if body := ts.get("/healthz"); body != "ok\n" {
		t.Errorf("healthz = %q", body)
	}
}

func grepLines(s, sub string) string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(line, sub) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func TestOthersSeeEachOther(t *testing.T) {
	t.Parallel()
	ts := startServer(t, store.NewMemory(time.Now))
	a, b := ts.dial(""), ts.dial("")
	wa := readUntil(t, a, anyMessage[*protocol.Welcome])
	wb := readUntil(t, b, anyMessage[*protocol.Welcome])
	if wa.YourID == wb.YourID {
		t.Fatal("two connections share an ID")
	}
	send(t, a, protocol.Join{Name: "ann", Color: protocol.ColorRandom})

	// b is a spectator and therefore sees a wherever it spawned.
	st := readUntil(t, b, func(st *protocol.State) bool { return len(st.Drops) == 1 })
	if st.Drops[0].ID != wa.YourID || st.Drops[0].Wetness2 < 190 {
		t.Errorf("b sees %+v", st.Drops)
	}

	// When a leaves, b hears a quiet death.
	_ = a.Close(websocket.StatusNormalClosure, "")
	readUntil(t, b, func(ev *protocol.Events) bool {
		for _, e := range ev.Events {
			if d, ok := e.(protocol.Death); ok && d.ID == wa.YourID && d.Cause == protocol.CauseLeft {
				return true
			}
		}
		return false
	})
}

func expectClosed(t *testing.T, conn *websocket.Conn, within time.Duration, want websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatalf("connection still open after %v", within)
			}
			if got := websocket.CloseStatus(err); want != 0 && got != want {
				t.Errorf("close status = %d (%v), want %d", got, err, want)
			}
			return
		}
	}
}

func TestSilentConnectionIsDropped(t *testing.T) {
	t.Parallel()
	ts := startServer(t, store.NewMemory(time.Now))
	conn := ts.dial("")
	start := time.Now()
	expectClosed(t, conn, idleTimeout+2*time.Second, 0)
	if elapsed := time.Since(start); elapsed < idleTimeout-200*time.Millisecond {
		t.Errorf("dropped after %v, want about %v", elapsed, idleTimeout)
	}
	deadline := time.Now().Add(2 * time.Second)
	for seated(ts.hub.page(1)) != (seatCount{}) {
		if time.Now().After(deadline) {
			t.Fatal("seat not released")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFloodingAndGarbageAreRejected(t *testing.T) {
	t.Parallel()
	ts := startServer(t, store.NewMemory(time.Now))

	flood := ts.dial("")
	for i := range maxMessagesPerSecond + 5 {
		_ = flood.Write(context.Background(), websocket.MessageBinary, protocol.Ping{ClientTime: uint32(i)}.Append(nil))
	}
	expectClosed(t, flood, 2*time.Second, websocket.StatusPolicyViolation)

	garbage := ts.dial("")
	_ = garbage.Write(context.Background(), websocket.MessageBinary, []byte{0x7f, 1, 2, 3})
	expectClosed(t, garbage, 2*time.Second, websocket.StatusProtocolError)

	text := ts.dial("")
	_ = text.Write(context.Background(), websocket.MessageText, []byte("hello"))
	expectClosed(t, text, 2*time.Second, websocket.StatusUnsupportedData)
}

type seatCount struct{ players, spectators int }

func seated(p *Page) seatCount {
	players, spectators := p.seats.count()
	return seatCount{players, spectators}
}

// setSeats overrides a page's seat counts, to fake a crowd.
func setSeats(p *Page, players, spectators int) {
	p.seats.mu.Lock()
	defer p.seats.mu.Unlock()
	p.seats.players, p.seats.spectators = players, spectators
}

// waitSeats waits for the page goroutine to bring the seat counts to want.
func waitSeats(t *testing.T, p *Page, want seatCount) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for seated(p) != want {
		if time.Now().After(deadline) {
			t.Fatalf("seats = %+v, want %+v", seated(p), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// keepAlive pings so that conn does not idle out while a test is busy.
func keepAlive(conn *websocket.Conn) {
	go func() {
		for conn.Write(context.Background(), websocket.MessageBinary, protocol.Ping{}.Append(nil)) == nil {
			time.Sleep(time.Second)
		}
	}()
}

func TestFullPageSpillsOntoANewOne(t *testing.T) {
	t.Parallel()
	ts := startServer(t, store.NewMemory(time.Now))

	var join joinResponse
	if err := json.Unmarshal([]byte(ts.get("/join")), &join); err != nil || join != (joinResponse{Page: 1, WS: "/ws?page=1"}) {
		t.Fatalf("join = %+v, %v", join, err)
	}

	for i := range game.PageCap {
		conn := ts.dial(join.WS[len("/ws"):])
		if w := readUntil(t, conn, anyMessage[*protocol.Welcome]); w.Page != 1 {
			t.Fatalf("landed on page %d while page 1 had room", w.Page)
		}
		send(t, conn, protocol.Join{Name: "crowd", Color: protocol.ColorRandom})
		waitSeats(t, ts.hub.page(1), seatCount{players: i + 1})
		keepAlive(conn)
	}

	// Asking for the full page lands on a new one instead.
	overflow := ts.dial("?page=1")
	if w := readUntil(t, overflow, anyMessage[*protocol.Welcome]); w.Page != 2 {
		t.Errorf("overflow connection landed on page %d, want 2", w.Page)
	}
	if err := json.Unmarshal([]byte(ts.get("/join")), &join); err != nil || join.Page != 2 {
		t.Errorf("join with page 1 full = %+v, %v; want page 2", join, err)
	}
	// The gauges follow the seats within a tick.
	time.Sleep(3 * time.Second / game.TickRate)
	body := ts.get("/metrics")
	for _, want := range []string{
		`wetpaint_players{page="1"} 60`, `wetpaint_spectators{page="1"} 0`,
		`wetpaint_players{page="2"} 0`, `wetpaint_spectators{page="2"} 1`,
		"wetpaint_pages 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, grepLines(body, "wetpaint_"))
		}
	}
}

func TestSpectatorsHaveTheirOwnCap(t *testing.T) {
	t.Parallel()
	ts := startServer(t, store.NewMemory(time.Now))
	for range game.SpectatorCap {
		conn := ts.dial("?page=1")
		if w := readUntil(t, conn, anyMessage[*protocol.Welcome]); w.Page != 1 {
			t.Fatalf("spectator landed on page %d while page 1 had room", w.Page)
		}
		keepAlive(conn)
	}
	if got, want := seated(ts.hub.page(1)), (seatCount{spectators: game.SpectatorCap}); got != want {
		t.Fatalf("seats = %+v, want %+v", got, want)
	}

	// No player has joined, yet the page takes nobody else.
	var join joinResponse
	if err := json.Unmarshal([]byte(ts.get("/join")), &join); err != nil || join.Page != 2 {
		t.Errorf("join with page 1 full of spectators = %+v, %v; want page 2", join, err)
	}
	late := ts.dial("?page=1")
	if w := readUntil(t, late, anyMessage[*protocol.Welcome]); w.Page != 2 {
		t.Errorf("extra spectator landed on page %d, want 2", w.Page)
	}
}

func TestJoinOnFullPageClosesWithPageFull(t *testing.T) {
	t.Parallel()
	ts := startServer(t, store.NewMemory(time.Now))
	page := ts.hub.page(1)
	a, b := ts.dial(""), ts.dial("")
	readUntil(t, a, anyMessage[*protocol.Welcome])
	readUntil(t, b, anyMessage[*protocol.Welcome])
	// Two spectators watch a page with one player seat left.
	setSeats(page, game.PageCap-1, 2)

	send(t, a, protocol.Join{Name: "first", Color: 0})
	waitSeats(t, page, seatCount{players: game.PageCap, spectators: 1})
	readUntil(t, a, func(st *protocol.State) bool { return st.Alive })

	send(t, b, protocol.Join{Name: "second", Color: 0})
	expectClosed(t, b, 2*time.Second, protocol.CloseFull)
	waitSeats(t, page, seatCount{players: game.PageCap})

	// The player who made it is unaffected, and can die and respawn in place.
	send(t, a, protocol.Ping{ClientTime: 1})
	readUntil(t, a, anyMessage[*protocol.Pong])
}

func TestJoinRoutesToOtherInstances(t *testing.T) {
	t.Parallel()
	s := store.NewMemory(time.Now)
	ts := startServer(t, s)
	// Fill the only local page, and advertise a roomy page elsewhere.
	setSeats(ts.hub.page(1), game.PageCap, 0)
	defer setSeats(ts.hub.page(1), 0, 0)
	ctx := context.Background()
	for _, info := range []store.PageInfo{
		{Page: 7, Addr: "wss://other.example/ws", Players: 12},
		{Page: 8, Addr: "wss://other.example/ws", Players: 40, Spectators: game.SpectatorCap - 1},
		{Page: 9, Addr: "wss://other.example/ws", Players: game.PageCap},
		{Page: 10, Addr: "", Players: 50}, // not routable
		{Page: 11, Addr: "wss://other.example/ws", Players: 45, Spectators: game.SpectatorCap},
	} {
		if err := s.Heartbeat(ctx, info); err != nil {
			t.Fatal(err)
		}
	}
	var join joinResponse
	if err := json.Unmarshal([]byte(ts.get("/join")), &join); err != nil {
		t.Fatal(err)
	}
	if want := (joinResponse{Page: 8, WS: "wss://other.example/ws?page=8"}); join != want {
		t.Errorf("join = %+v, want %+v", join, want)
	}
}

func TestPaintSurvivesRestart(t *testing.T) {
	t.Parallel()
	s := store.NewMemory(time.Now)
	ts := startServer(t, s)
	conn := ts.dial("")
	readUntil(t, conn, anyMessage[*protocol.Welcome])
	send(t, conn, protocol.Join{Name: "painter", Color: 0})
	var lastPaintTick uint32
	for seq := uint32(1); lastPaintTick == 0; seq++ {
		send(t, conn, protocol.Input{Seq: seq, DX: 0, DY: 127})
		readUntil(t, conn, func(ev *protocol.Events) bool {
			if len(ev.Paint) > 0 {
				lastPaintTick = ev.Tick
			}
			return true
		})
	}
	ts.stop() // saves a final snapshot

	ts2 := startServer(t, s)
	conn2 := ts2.dial("")
	welcome := readUntil(t, conn2, anyMessage[*protocol.Welcome])
	if welcome.Tick < lastPaintTick {
		t.Errorf("restarted at tick %d, before the saved tick %d", welcome.Tick, lastPaintTick)
	}
	planes, err := paint.Decompress(welcome.Grid, game.GridW*game.GridH*paint.SnapshotBytesPerCell)
	if err != nil {
		t.Fatal(err)
	}
	cells := game.GridW * game.GridH
	paintedCells := 0
	for _, a := range planes[3*cells : 4*cells] {
		if a > 0 {
			paintedCells++
		}
	}
	if paintedCells == 0 {
		t.Error("restored page is blank; the trail should have survived the restart")
	}
}

func TestSnapshotEncoding(t *testing.T) {
	t.Parallel()
	planes := make([]byte, game.GridW*game.GridH*paint.SnapshotBytesPerCell)
	planes[5], planes[len(planes)-1] = 9, 7
	tick, back, err := decodeSnapshot(encodeSnapshot(123456, planes))
	if err != nil || tick != 123456 || !bytes.Equal(back, planes) {
		t.Errorf("round trip: tick %d, err %v, equal %v", tick, err, bytes.Equal(back, planes))
	}
	for _, bad := range [][]byte{nil, {1, 2}, {1, 2, 3, 4, 5, 6}} {
		if _, _, err := decodeSnapshot(bad); err == nil {
			t.Errorf("decodeSnapshot(%v) succeeded", bad)
		}
	}
}

func TestSlowClientLosesStatesNotEvents(t *testing.T) {
	t.Parallel()
	q := newSendQueue()
	push := func(id byte, droppable bool) (ok, dropped bool) {
		return q.push(outbound{data: []byte{id}, droppable: droppable})
	}
	// R = reliable, D = droppable, in the order a page might produce them.
	for id, droppable := range []bool{false, true, true, true, true, false} {
		if ok, dropped := push(byte(id), droppable); !ok || dropped {
			t.Fatalf("message %d: ok %v, dropped %v", id, ok, dropped)
		}
	}
	// One droppable message too many pushes out the oldest of its kind…
	if ok, dropped := push(6, true); !ok || !dropped {
		t.Fatalf("overflowing droppable message: ok %v, dropped %v", ok, dropped)
	}
	// …and everything else still leaves in the order it was produced.
	var got []byte
	for {
		msg, ok := q.pop()
		if !ok {
			break
		}
		got = append(got, msg.data[0])
	}
	if want := []byte{0, 2, 3, 4, 5, 6}; !bytes.Equal(got, want) {
		t.Errorf("popped %v, want %v", got, want)
	}

	// Droppable messages never crowd out reliable ones, nor the reverse.
	for range droppableQueue * 3 {
		push(0, true)
	}
	for range reliableQueue {
		if ok, _ := push(0, false); !ok {
			t.Fatal("reliable message refused before the queue was full")
		}
	}
	if ok, _ := push(0, false); ok {
		t.Error("reliable message accepted beyond the queue's capacity")
	}
	if ok, _ := push(0, true); !ok {
		t.Error("droppable message refused")
	}
	if len(q.items) != reliableQueue+droppableQueue {
		t.Errorf("queue holds %d messages, want %d", len(q.items), reliableQueue+droppableQueue)
	}
}
