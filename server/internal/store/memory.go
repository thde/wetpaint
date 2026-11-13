package store

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/thde/wetpaint/server/internal/protocol"
)

// Memory is the Store used when Valkey is disabled. Its directory and hourly
// boards work like the Valkey ones; it keeps no global boards, and its
// snapshots live only as long as the process.
type Memory struct {
	now func() time.Time

	mu        sync.Mutex
	pages     map[uint32]memoryPage
	scores    map[scoreKey][]timedEntry
	snapshots map[uint32][]byte
}

type memoryPage struct {
	info    PageInfo
	expires time.Time
}

type scoreKey struct {
	page  uint32
	board Board
}

type timedEntry struct {
	entry protocol.Entry
	at    time.Time
}

// NewMemory returns an empty in-process store. now is the clock used for
// directory expiry; pass time.Now outside tests.
func NewMemory(now func() time.Time) *Memory {
	return &Memory{
		now:       now,
		pages:     make(map[uint32]memoryPage),
		scores:    make(map[scoreKey][]timedEntry),
		snapshots: make(map[uint32][]byte),
	}
}

// ClaimPage implements Store.
func (m *Memory) ClaimPage(_ context.Context, addr string) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for page := uint32(1); ; page++ {
		if p, ok := m.pages[page]; ok && now.Before(p.expires) {
			continue
		}
		m.pages[page] = memoryPage{info: PageInfo{Page: page, Addr: addr}, expires: now.Add(PageTTL)}
		return page, nil
	}
}

// Heartbeat implements Store.
func (m *Memory) Heartbeat(_ context.Context, info PageInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pages[info.Page] = memoryPage{info: info, expires: m.now().Add(PageTTL)}
	return nil
}

// RemovePage implements Store.
func (m *Memory) RemovePage(_ context.Context, page uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pages, page)
	return nil
}

// Pages implements Store.
func (m *Memory) Pages(context.Context) ([]PageInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	var out []PageInfo
	for page, p := range m.pages {
		if !now.Before(p.expires) {
			delete(m.pages, page)
			continue
		}
		out = append(out, p.info)
	}
	slices.SortFunc(out, func(a, b PageInfo) int { return cmp.Compare(a.Page, b.Page) })
	return out, nil
}

// RecordScore implements Store.
func (m *Memory) RecordScore(_ context.Context, page uint32, board Board, entry protocol.Entry, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := scoreKey{page, board}
	m.scores[key] = append(fresh(m.scores[key], at), timedEntry{entry, at})
	return nil
}

// Top implements Store.
func (m *Memory) Top(_ context.Context, page uint32, n int, now time.Time) (Tops, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var tops Tops
	for board := range NumBoards {
		key := scoreKey{page, board}
		m.scores[key] = fresh(m.scores[key], now)
		entries := make([]protocol.Entry, len(m.scores[key]))
		for i, s := range m.scores[key] {
			entries[i] = s.entry
		}
		slices.SortStableFunc(entries, func(a, b protocol.Entry) int { return cmp.Compare(b.Value, a.Value) })
		tops.Hour[board] = bestPerName(entries, n)
	}
	return tops, nil
}

// fresh drops scores older than the hour window, in place.
func fresh(scores []timedEntry, now time.Time) []timedEntry {
	cutoff := now.Add(-HourWindow)
	return slices.DeleteFunc(scores, func(s timedEntry) bool { return !s.at.After(cutoff) })
}

// SaveSnapshot implements Store.
func (m *Memory) SaveSnapshot(_ context.Context, page uint32, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots[page] = slices.Clone(data)
	return nil
}

// LoadSnapshot implements Store.
func (m *Memory) LoadSnapshot(_ context.Context, page uint32) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.snapshots[page]), nil
}

// Close implements Store.
func (m *Memory) Close() error { return nil }
