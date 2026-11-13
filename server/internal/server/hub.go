// Package server is the network face of Wet Paint: it accepts WebSocket
// connections, routes them to pages, runs each page's tick loop and exposes
// the HTTP endpoints around the game.
package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/thde/wetpaint/server/internal/store"
)

var errNewPageFull = errors.New("new page is already full")

const (
	directoryInterval = 2 * time.Second
	storeTimeout      = 3 * time.Second
)

// Config is the static configuration of a Hub.
type Config struct {
	// PublicURL is the WebSocket URL other instances should hand to players
	// routed to this one, e.g. "wss://eu1.example.com/ws". Empty means this
	// instance is only reachable through its own /join.
	PublicURL string
	// OriginPatterns are the extra Origin hosts allowed to open WebSockets;
	// same-origin requests are always allowed.
	OriginPatterns []string
	// StaticDir, if set, is served at / (the built client).
	StaticDir string
}

// Hub owns the pages of one server instance.
type Hub struct {
	cfg     Config
	store   store.Store
	log     *slog.Logger
	metrics *hubMetrics
	persist *persister

	ctx    context.Context //nolint:containedctx // lifetime of every page, set by Start; requests derive from their own context
	pageWG sync.WaitGroup
	bgWG   sync.WaitGroup

	createMu sync.Mutex // serialises page creation
	mu       sync.Mutex // guards pages
	pages    map[uint32]*Page
}

// NewHub returns a Hub that keeps shared state in s. Call Start before
// serving its Handler.
func NewHub(cfg Config, s store.Store, log *slog.Logger) *Hub {
	m := newMetrics()
	return &Hub{
		cfg:     cfg,
		store:   s,
		log:     log,
		metrics: m,
		persist: newPersister(s, log, m),
		pages:   make(map[uint32]*Page),
	}
}

// Start launches the background work and opens the first page. Everything
// stops when ctx ends; Wait then blocks until it has.
func (h *Hub) Start(ctx context.Context) error {
	h.ctx = ctx
	h.bgWG.Go(h.persist.run)
	h.bgWG.Go(h.directoryLoop)
	if _, err := h.createPage(ctx); err != nil {
		return fmt.Errorf("open first page: %w", err)
	}
	return nil
}

// Wait blocks until every page has stopped and queued store writes,
// including the final snapshots, are done.
func (h *Hub) Wait() {
	<-h.ctx.Done()
	h.pageWG.Wait()
	close(h.persist.jobs)
	h.bgWG.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	for _, p := range h.localPages() {
		if err := h.store.RemovePage(ctx, p.id); err != nil {
			h.log.Warn("could not leave the page directory", "page", p.id, "err", err)
		}
	}
}

// localPages returns this instance's pages, the one with most players first.
func (h *Hub) localPages() []*Page {
	h.mu.Lock()
	pages := make([]*Page, 0, len(h.pages))
	for _, p := range h.pages {
		pages = append(pages, p)
	}
	h.mu.Unlock()
	players := make(map[*Page]int, len(pages))
	for _, p := range pages {
		players[p], _ = p.seats.count()
	}
	slices.SortFunc(pages, func(a, b *Page) int {
		return cmp.Or(cmp.Compare(players[b], players[a]), cmp.Compare(a.id, b.id))
	})
	return pages
}

func (h *Hub) page(id uint32) *Page {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pages[id]
}

// reserve seats a new connection as a spectator: on the wanted page if it
// has room, else on the fullest page that does, else on a new page.
func (h *Hub) reserve(ctx context.Context, want uint32) (*Page, error) {
	if p := h.reserveExisting(want); p != nil {
		return p, nil
	}
	h.createMu.Lock()
	defer h.createMu.Unlock()
	// Someone else may have made room while we waited for the lock.
	if p := h.reserveExisting(want); p != nil {
		return p, nil
	}
	p, err := h.createPageLocked(ctx)
	if err != nil {
		return nil, err
	}
	if !p.seats.reserve() {
		return nil, errNewPageFull
	}
	return p, nil
}

func (h *Hub) reserveExisting(want uint32) *Page {
	if p := h.page(want); p != nil && p.seats.reserve() {
		return p
	}
	for _, p := range h.localPages() {
		if p.seats.reserve() {
			return p
		}
	}
	return nil
}

func (h *Hub) createPage(ctx context.Context) (*Page, error) {
	h.createMu.Lock()
	defer h.createMu.Unlock()
	return h.createPageLocked(ctx)
}

// createPageLocked claims a page number, restores its paint if a snapshot
// exists and starts its goroutine. The caller holds createMu.
func (h *Hub) createPageLocked(ctx context.Context) (*Page, error) {
	if err := h.ctx.Err(); err != nil {
		return nil, fmt.Errorf("server is shutting down: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()

	id, err := h.store.ClaimPage(ctx, h.cfg.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("claim a page number: %w", err)
	}
	log := h.log.With("page", id)
	snapshot, err := h.store.LoadSnapshot(ctx, id)
	if err != nil {
		log.Warn("could not load snapshot; starting with a blank sheet", "err", err)
	}
	newRand := func() *rand.Rand { return rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) }
	p := newPage(id, h, restoreWorld(newRand, snapshot, log))

	h.mu.Lock()
	h.pages[id] = p
	h.metrics.pages.Set(float64(len(h.pages)))
	h.mu.Unlock()

	h.pageWG.Go(func() { p.run(h.ctx) })
	log.Info("page opened")
	return p, nil
}

// retire removes an empty page from the hub. It fails if somebody reserved
// a seat in the meantime.
func (h *Hub) retire(p *Page) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !p.seats.retire() {
		return false
	}
	delete(h.pages, p.id)
	h.metrics.pages.Set(float64(len(h.pages)))
	// Before RemovePage frees the number, so a new page can't lose its series.
	h.metrics.dropPage(p.id)
	id := p.id
	h.persist.submit(job{what: "remove page", run: func(ctx context.Context, s store.Store) error {
		return s.RemovePage(ctx, id)
	}})
	return true
}

// directoryLoop keeps this instance's pages alive in the directory and
// fetches their stored leaderboards, so pages never touch the store.
func (h *Hub) directoryLoop() {
	ticker := time.NewTicker(directoryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
		}
		for _, p := range h.localPages() {
			ctx, cancel := context.WithTimeout(h.ctx, storeTimeout)
			players, spectators := p.seats.count()
			err := h.store.Heartbeat(ctx, store.PageInfo{Page: p.id, Addr: h.cfg.PublicURL, Players: players, Spectators: spectators})
			if err == nil {
				var tops store.Tops
				if tops, err = h.store.Top(ctx, p.id, boardSize, time.Now()); err == nil {
					p.offerTops(tops)
				}
			}
			cancel()
			if err != nil && h.ctx.Err() == nil {
				h.metrics.persistErrors.Inc()
				h.log.Warn("directory refresh failed", "page", p.id, "err", err)
			}
		}
	}
}
