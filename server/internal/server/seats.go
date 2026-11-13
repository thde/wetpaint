package server

import (
	"sync"

	"github.com/thde/wetpaint/server/internal/game"
)

// seats counts the connections of one page: players, who have sent Join and
// keep their seat while dead, and spectators, who have not. It is the one
// piece of a page that other goroutines touch (HTTP handlers reserve seats
// before the page goroutine hears of the connection), hence the mutex; it is
// admission bookkeeping, not game state.
type seats struct {
	mu         sync.Mutex
	players    int
	spectators int
	retired    bool // the page is gone and takes nobody
}

// roomLocked reports whether a newcomer could both watch and then join.
func (s *seats) roomLocked() bool {
	return !s.retired && s.players < game.PageCap && s.spectators < game.SpectatorCap
}

// hasRoom reports whether the page takes new connections.
func (s *seats) hasRoom() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.roomLocked()
}

// reserve seats a new connection as a spectator, failing if the page has no
// room for it or for the player it wants to become.
func (s *seats) reserve() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.roomLocked() {
		return false
	}
	s.spectators++
	return true
}

// join turns a spectator into a player, failing if the page already has all
// its players.
func (s *seats) join() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.players >= game.PageCap {
		return false
	}
	s.spectators--
	s.players++
	return true
}

// leave frees the seat of a connection that had or had not joined.
func (s *seats) leave(joined bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if joined {
		s.players--
	} else {
		s.spectators--
	}
}

func (s *seats) count() (players, spectators int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.players, s.spectators
}

// retire closes an empty page to newcomers for good. It fails if somebody
// holds a seat.
func (s *seats) retire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.players+s.spectators > 0 {
		return false
	}
	s.retired = true
	return true
}
