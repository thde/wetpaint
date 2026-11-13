package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/coder/websocket"

	"github.com/thde/wetpaint/server/internal/game"
)

// Handler returns the instance's HTTP routes:
//
//	GET /ws[?page=N]  the game WebSocket
//	GET /join         JSON routing to a page with room
//	GET /healthz      liveness
//	GET /metrics      Prometheus
//	GET /             the built client, if Config.StaticDir is set
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", h.handleWS)
	mux.HandleFunc("GET /join", h.handleJoin)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /metrics", h.metrics)
	if h.cfg.StaticDir != "" {
		mux.Handle("GET /", http.FileServer(http.Dir(h.cfg.StaticDir)))
	}
	return mux
}

// joinResponse tells a client where to connect.
type joinResponse struct {
	Page uint32 `json:"page"`
	WS   string `json:"ws"` // same-origin path, or absolute URL of another instance
}

// handleJoin routes a new player: to the fullest local page with room, else
// to another instance's, else to a freshly opened local page. A page has
// room while it can take both another spectator and another player.
func (h *Hub) handleJoin(w http.ResponseWriter, r *http.Request) {
	resp, err := h.route(r.Context())
	if err != nil {
		h.log.Error("join routing failed", "err", err)
		http.Error(w, "no page available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Hub) route(ctx context.Context) (joinResponse, error) {
	local := func(id uint32) joinResponse {
		return joinResponse{Page: id, WS: "/ws?page=" + strconv.FormatUint(uint64(id), 10)}
	}
	for _, p := range h.localPages() { // fullest first
		if p.seats.hasRoom() {
			return local(p.id), nil
		}
	}

	sctx, cancel := context.WithTimeout(ctx, storeTimeout)
	infos, err := h.store.Pages(sctx)
	cancel()
	if err != nil {
		// The directory is a nicety; a local page always works.
		h.log.Warn("page directory unavailable", "err", err)
	}
	var best *joinResponse
	bestPlayers := -1
	for _, info := range infos {
		if info.Addr == "" || info.Addr == h.cfg.PublicURL || h.page(info.Page) != nil {
			continue
		}
		hasRoom := info.Players < game.PageCap && info.Spectators < game.SpectatorCap
		if hasRoom && info.Players > bestPlayers {
			bestPlayers = info.Players
			best = &joinResponse{Page: info.Page, WS: fmt.Sprintf("%s?page=%d", info.Addr, info.Page)}
		}
	}
	if best != nil {
		return *best, nil
	}

	p, err := h.createPage(ctx)
	if err != nil {
		return joinResponse{}, err
	}
	return local(p.id), nil
}

// handleWS upgrades the request and runs the connection until it ends.
func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	want, _ := strconv.ParseUint(r.URL.Query().Get("page"), 10, 32)
	page, err := h.reserve(r.Context(), uint32(want))
	if err != nil {
		h.log.Error("no page for connection", "err", err)
		http.Error(w, "no page available", http.StatusServiceUnavailable)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  h.cfg.OriginPatterns,
		CompressionMode: websocket.CompressionDisabled, // payloads are already compact
	})
	if err != nil {
		page.seats.leave(false)
		return // Accept has written the HTTP error
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxFrameBytes)

	// The connection ends with the request, the hub, or a kick.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(h.ctx, cancel)() //nolint:contextcheck // also end the connection when the hub stops

	c := newClient(conn, cancel)
	if !page.send(ctx, command{kind: cmdConnect, client: c}) {
		page.seats.leave(false)
		return
	}
	go c.writeLoop(ctx, h.metrics)
	status, reason := c.readLoop(ctx, page)

	// Always tell the page, even though ctx may be done by now: it frees the
	// seat, which only the page knows to be a player's or a spectator's.
	select {
	case page.inbox <- command{kind: cmdDisconnect, client: c}:
	case <-page.done:
	}
	cancel()
	_ = conn.Close(status, reason)
}
