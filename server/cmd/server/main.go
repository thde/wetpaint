// Command server runs a Wet Paint instance: the game WebSocket, the join
// router, health and metrics endpoints and, optionally, the built client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/thde/wetpaint/server/internal/memlimit"
	"github.com/thde/wetpaint/server/internal/server"
	"github.com/thde/wetpaint/server/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, os.Args[1:]); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	var (
		addr       = fs.String("addr", defaultAddr(os.Getenv), "HTTP listen address (default :$PORT if set, else :8080)")
		static     = fs.String("static", defaultStatic(os.Getenv), "directory with the built client to serve at / (default $KO_DATA_PATH if set, else none)")
		valkeyAddr = fs.String("valkey", "", "Valkey host:port for the page directory, leaderboards and snapshots (empty: a Deploio Key-Value Store from NINE_KVS_* if attached, else in-memory)")
		publicURL  = fs.String("public-url", "", "this instance's public WebSocket URL, e.g. wss://eu1.example.com/ws, so other instances can route players here (needs -valkey)")
		origins    = fs.String("origins", "localhost:*,127.0.0.1:*,localhost,127.0.0.1", "comma-separated host patterns allowed as WebSocket Origin, besides same-origin")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	memlimit.Set(ctx, log)

	// An explicit -valkey wins over a Key-Value Store found in the environment.
	var (
		valkeyOpts store.ValkeyOptions
		useValkey  bool
	)
	if *valkeyAddr != "" {
		valkeyOpts, useValkey = store.ValkeyOptions{Addr: *valkeyAddr}, true
	} else {
		opts, name, ok, err := kvsFromEnv(os.Environ())
		if err != nil {
			return err
		}
		if ok {
			valkeyOpts, useValkey = opts, true
			log.Info("found Key-Value Store in environment", "name", name)
		}
	}

	var st store.Store = store.NewMemory(time.Now)
	if useValkey {
		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		vs, err := store.NewValkey(connectCtx, valkeyOpts)
		cancel()
		if err != nil {
			return err
		}
		st = vs
		log.Info("using valkey", "addr", valkeyOpts.Addr, "tls", valkeyOpts.TLS != nil)
	}
	defer st.Close()

	hub := server.NewHub(server.Config{
		PublicURL:      *publicURL,
		OriginPatterns: splitList(*origins),
		StaticDir:      *static,
	}, st, log)
	if err := hub.Start(ctx); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           hub.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	log.Info("listening", "addr", *addr)

	var err error
	select {
	case err = <-serveErr:
		stop() // the listener failed; shut the hub down too
		err = fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sErr := srv.Shutdown(shutdownCtx); sErr != nil && err == nil {
		err = fmt.Errorf("shut down http: %w", sErr)
	}
	hub.Wait()
	return err
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
