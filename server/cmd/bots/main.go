// Command bots fills a Wet Paint server with simple players for testing
// crowded pages and load.
//
//	go run ./cmd/bots -n 30 -url ws://localhost:8080/ws
//
// Without -url the bots connect to $WETPAINT_URL if set, else
// ws://localhost:8080/ws.
//
// Bots wander, head for water when they dry, flee drops big enough to absorb
// them, occasionally chase smaller ones, and respawn when they die.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/thde/wetpaint/server/internal/memlimit"
)

var (
	errUsage    = errors.New("usage")
	errProtocol = errors.New("protocol")
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, os.Args[1:]); err != nil {
		log.Error("bots stopped", "err", err)
		os.Exit(1)
	}
}

// defaultURL is the server WebSocket URL used when -url is not given:
// $WETPAINT_URL if set, else a local server.
func defaultURL(getenv func(string) string) string {
	if url := getenv("WETPAINT_URL"); url != "" {
		return url
	}
	return "ws://localhost:8080/ws"
}

// stats are shared by all bots and reported periodically.
type stats struct {
	connected  atomic.Int64
	alive      atomic.Int64
	deaths     atomic.Int64
	lifeMillis atomic.Int64 // total length of finished lives
	reconnects atomic.Int64
}

func run(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("bots", flag.ContinueOnError)
	var (
		n      = fs.Int("n", 30, "number of bots")
		url    = fs.String("url", defaultURL(os.Getenv), "server WebSocket URL (default $WETPAINT_URL if set, else ws://localhost:8080/ws)")
		ramp   = fs.Duration("ramp", 20*time.Millisecond, "delay between connecting bots")
		report = fs.Duration("report", 10*time.Second, "interval between status lines (0: quiet)")
		lazy   = fs.Bool("lazy", false, "bots never seek water (to measure an unrefilled life)")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if *n < 1 {
		return fmt.Errorf("%w: -n must be at least 1, got %d", errUsage, *n)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	memlimit.Set(ctx, log)

	var (
		st stats
		wg sync.WaitGroup
	)
	for i := range *n {
		b := &bot{
			name:  fmt.Sprintf("bot %d", i+1),
			seed:  rand.Uint32(),
			url:   *url,
			lazy:  *lazy,
			rng:   rand.New(rand.NewPCG(rand.Uint64(), uint64(i))),
			stats: &st,
			log:   log,
		}
		wg.Go(func() { b.runForever(ctx) })
		select {
		case <-ctx.Done():
		case <-time.After(*ramp):
		}
	}

	if *report > 0 {
		wg.Go(func() {
			ticker := time.NewTicker(*report)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					deaths := st.deaths.Load()
					meanLife := 0.0
					if deaths > 0 {
						meanLife = float64(st.lifeMillis.Load()) / float64(deaths) / 1000
					}
					log.Info("bots",
						"connected", st.connected.Load(), "alive", st.alive.Load(),
						"deaths", deaths, "mean_life_s", fmt.Sprintf("%.1f", meanLife),
						"reconnects", st.reconnects.Load())
				}
			}
		})
	}
	wg.Wait()
	return nil
}
