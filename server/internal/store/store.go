// Package store holds everything that outlives a page goroutine or is shared
// between server instances: the page directory, leaderboards and paint grid
// snapshots. The game never calls a Store from its tick loop.
package store

import (
	"context"
	"time"

	"github.com/thde/wetpaint/server/internal/protocol"
)

// Board identifies one leaderboard category.
type Board uint8

// Leaderboard categories, in the order the protocol lists them.
const (
	Painted Board = iota // cells painted in one life
	Longest              // seconds alive
	Mixed                // colours mixed in one life
	NumBoards
)

func (b Board) String() string {
	switch b {
	case Painted:
		return "painted"
	case Longest:
		return "longest"
	case Mixed:
		return "mixed"
	default:
		return "unknown"
	}
}

const (
	// HourWindow is how long a score stays on a page's "last hour" board.
	HourWindow = time.Hour
	// PageTTL is how long a page stays in the directory without a heartbeat.
	PageTTL = 10 * time.Second
	// SnapshotTTL is how long a saved paint grid is worth restoring.
	SnapshotTTL = time.Hour
	// GlobalKeep is how many entries each global board retains.
	GlobalKeep = 100
)

// PageInfo is a directory entry: where a page lives and how full it is.
type PageInfo struct {
	Page    uint32 `json:"page"`
	Addr    string `json:"addr"`    // public WebSocket URL of the owning instance; may be empty
	Players int    `json:"players"` // connections that have joined
	// Spectators are connections that have not joined yet. A page takes
	// newcomers only while it has room for both kinds.
	Spectators int `json:"spectators"`
}

// Tops is the persistent half of a leaderboard: this page's last hour and
// the global boards.
type Tops struct {
	Hour   [NumBoards][]protocol.Entry
	Global [NumBoards][]protocol.Entry
}

// Store is implemented by [Memory] for a single instance and [Valkey] for a
// fleet. Implementations are safe for concurrent use.
type Store interface {
	// ClaimPage reserves the lowest page number nobody is serving and
	// registers it in the directory for addr.
	ClaimPage(ctx context.Context, addr string) (uint32, error)
	// Heartbeat refreshes a page's directory entry and its TTL.
	Heartbeat(ctx context.Context, info PageInfo) error
	// RemovePage drops a page from the directory at once.
	RemovePage(ctx context.Context, page uint32) error
	// Pages lists every live page of every instance.
	Pages(ctx context.Context) ([]PageInfo, error)

	// RecordScore adds one finished life to a page's hourly board and to
	// the global board.
	RecordScore(ctx context.Context, page uint32, board Board, entry protocol.Entry, at time.Time) error
	// Top returns the best n entries per board, one per name.
	Top(ctx context.Context, page uint32, n int, now time.Time) (Tops, error)

	// SaveSnapshot stores a page's paint grid, replacing the previous one.
	SaveSnapshot(ctx context.Context, page uint32, data []byte) error
	// LoadSnapshot returns the stored grid, or nil if there is none.
	LoadSnapshot(ctx context.Context, page uint32) ([]byte, error)

	Close() error
}

// bestPerName keeps the first (best) entry of each name, up to n entries.
// entries must be sorted best first.
func bestPerName(entries []protocol.Entry, n int) []protocol.Entry {
	var out []protocol.Entry
	seen := make(map[string]bool, n)
	for _, e := range entries {
		if len(out) == n {
			break
		}
		if !seen[e.Name] {
			seen[e.Name] = true
			out = append(out, e)
		}
	}
	return out
}
