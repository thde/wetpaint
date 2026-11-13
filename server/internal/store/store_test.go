package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/thde/wetpaint/server/internal/protocol"
)

// fakeClock drives both the Memory clock and miniredis' TTLs.
type fakeClock struct {
	now     time.Time
	advance func(time.Duration)
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
	if c.advance != nil {
		c.advance(d)
	}
}

// eachStore runs a test against both implementations.
func eachStore(t *testing.T, test func(t *testing.T, s Store, clock *fakeClock, global bool)) {
	t.Helper()
	start := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	t.Run("memory", func(t *testing.T) {
		clock := &fakeClock{now: start}
		test(t, NewMemory(clock.Now), clock, false)
	})

	t.Run("valkey", func(t *testing.T) {
		mr := miniredis.RunT(t)
		clock := &fakeClock{now: start, advance: mr.FastForward}
		s, err := NewValkey(context.Background(), ValkeyOptions{Addr: mr.Addr()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		test(t, s, clock, true)
	})
}

func TestPageDirectory(t *testing.T) {
	t.Parallel()
	eachStore(t, func(t *testing.T, s Store, clock *fakeClock, _ bool) { //nolint:thelper // test body, not a helper
		ctx := context.Background()
		for want := uint32(1); want <= 3; want++ {
			got, err := s.ClaimPage(ctx, "ws://a/ws")
			if err != nil || got != want {
				t.Fatalf("ClaimPage = %d, %v; want %d", got, err, want)
			}
		}
		if err := s.Heartbeat(ctx, PageInfo{Page: 2, Addr: "ws://a/ws", Players: 41, Spectators: 7}); err != nil {
			t.Fatal(err)
		}
		if err := s.RemovePage(ctx, 1); err != nil {
			t.Fatal(err)
		}

		pages, err := s.Pages(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[uint32]PageInfo{}
		for _, p := range pages {
			got[p.Page] = p
		}
		want := map[uint32]PageInfo{
			2: {Page: 2, Addr: "ws://a/ws", Players: 41, Spectators: 7},
			3: {Page: 3, Addr: "ws://a/ws"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Pages = %+v, want %+v", got, want)
		}

		// The freed number is handed out again.
		if page, _ := s.ClaimPage(ctx, "ws://b/ws"); page != 1 {
			t.Errorf("ClaimPage after removal = %d, want 1", page)
		}

		// Pages without heartbeats fall out of the directory.
		clock.Advance(PageTTL / 2)
		if err := s.Heartbeat(ctx, PageInfo{Page: 3, Addr: "ws://a/ws", Players: 7}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(PageTTL/2 + time.Second)
		pages, err = s.Pages(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 1 || pages[0].Page != 3 || pages[0].Players != 7 {
			t.Errorf("after TTL, Pages = %+v; want only page 3", pages)
		}
	})
}

func TestLeaderboards(t *testing.T) {
	t.Parallel()
	eachStore(t, func(t *testing.T, s Store, clock *fakeClock, global bool) { //nolint:thelper // test body, not a helper
		ctx := context.Background()
		red, blue := protocol.RGB{R: 200, G: 10, B: 1}, protocol.RGB{B: 255}
		record := func(page uint32, board Board, name string, value uint32, c protocol.RGB) {
			t.Helper()
			e := protocol.Entry{Name: name, Value: value, Color: c}
			if err := s.RecordScore(ctx, page, board, e, clock.Now()); err != nil {
				t.Fatal(err)
			}
			clock.Advance(time.Second)
		}

		record(1, Painted, "old", 9999, red)
		clock.Advance(50 * time.Minute)
		record(1, Painted, "ann", 300, red)
		record(1, Painted, "ann", 500, red) // a better life by the same name
		record(1, Painted, "bob|x", 400, blue)
		record(1, Painted, "cy", 100, blue)
		record(1, Longest, "ann", 71, red)
		record(2, Painted, "dee", 777, blue)

		tops, err := s.Top(ctx, 1, 2, clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		wantHour := []protocol.Entry{{Name: "old", Value: 9999, Color: red}, {Name: "ann", Value: 500, Color: red}}
		if !reflect.DeepEqual(tops.Hour[Painted], wantHour) {
			t.Errorf("hour painted = %+v, want %+v", tops.Hour[Painted], wantHour)
		}
		if want := []protocol.Entry{{Name: "ann", Value: 71, Color: red}}; !reflect.DeepEqual(tops.Hour[Longest], want) {
			t.Errorf("hour longest = %+v, want %+v", tops.Hour[Longest], want)
		}
		if len(tops.Hour[Mixed]) != 0 {
			t.Errorf("hour mixed = %+v, want empty", tops.Hour[Mixed])
		}

		// Twenty minutes on, "old" is more than an hour ago.
		clock.Advance(20 * time.Minute)
		tops, err = s.Top(ctx, 1, 5, clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		wantHour = []protocol.Entry{
			{Name: "ann", Value: 500, Color: red},
			{Name: "bob|x", Value: 400, Color: blue},
			{Name: "cy", Value: 100, Color: blue},
		}
		if !reflect.DeepEqual(tops.Hour[Painted], wantHour) {
			t.Errorf("hour painted after expiry = %+v, want %+v", tops.Hour[Painted], wantHour)
		}

		if !global {
			if len(tops.Global[Painted]) != 0 {
				t.Errorf("memory store has global entries: %+v", tops.Global[Painted])
			}
			return
		}
		// The global board spans pages and never expires.
		wantGlobal := []protocol.Entry{
			{Name: "old", Value: 9999, Color: red},
			{Name: "dee", Value: 777, Color: blue},
			{Name: "ann", Value: 500, Color: red},
			{Name: "bob|x", Value: 400, Color: blue},
			{Name: "cy", Value: 100, Color: blue},
		}
		if !reflect.DeepEqual(tops.Global[Painted], wantGlobal) {
			t.Errorf("global painted = %+v, want %+v", tops.Global[Painted], wantGlobal)
		}
	})
}

func TestSnapshots(t *testing.T) {
	t.Parallel()
	eachStore(t, func(t *testing.T, s Store, _ *fakeClock, _ bool) { //nolint:thelper // test body, not a helper
		ctx := context.Background()
		if data, err := s.LoadSnapshot(ctx, 4); err != nil || data != nil {
			t.Fatalf("LoadSnapshot of nothing = %v, %v; want nil, nil", data, err)
		}
		want := []byte{0, 1, 2, 255, 0}
		if err := s.SaveSnapshot(ctx, 4, []byte("first")); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveSnapshot(ctx, 4, want); err != nil {
			t.Fatal(err)
		}
		got, err := s.LoadSnapshot(ctx, 4)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("LoadSnapshot = %v, %v; want %v", got, err, want)
		}
	})
}

func TestNewValkeyFailsWithoutServer(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := NewValkey(ctx, ValkeyOptions{Addr: "127.0.0.1:1"}); err == nil {
		t.Error("NewValkey succeeded with nothing listening")
	}
}

func TestNewValkeyAuth(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("app", "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := NewValkey(ctx, ValkeyOptions{Addr: mr.Addr(), Username: "app", Password: "wrong"}); err == nil {
		t.Error("NewValkey succeeded with a wrong password")
	}
	v, err := NewValkey(ctx, ValkeyOptions{Addr: mr.Addr(), Username: "app", Password: "secret"})
	if err != nil {
		t.Fatalf("NewValkey with credentials: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Error(err)
	}
}
