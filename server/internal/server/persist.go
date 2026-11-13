package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/thde/wetpaint/server/internal/game"
	"github.com/thde/wetpaint/server/internal/paint"
	"github.com/thde/wetpaint/server/internal/store"
)

var errSnapshotShort = errors.New("snapshot too short")

const (
	persistQueue   = 256
	persistTimeout = 5 * time.Second
)

// job is one store operation queued by a page.
type job struct {
	what string
	run  func(ctx context.Context, s store.Store) error
}

// persister performs store writes on behalf of page goroutines, which must
// never wait on the network. When its queue is full, work is dropped.
type persister struct {
	store   store.Store
	jobs    chan job
	log     *slog.Logger
	metrics *hubMetrics
}

func newPersister(s store.Store, log *slog.Logger, m *hubMetrics) *persister {
	return &persister{store: s, jobs: make(chan job, persistQueue), log: log, metrics: m}
}

// submit queues a job without blocking, dropping it if the queue is full.
func (p *persister) submit(j job) {
	select {
	case p.jobs <- j:
	default:
		p.metrics.persistDropped.Inc()
	}
}

// run works through the queue until it is closed.
func (p *persister) run() {
	for j := range p.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		if err := j.run(ctx, p.store); err != nil {
			p.metrics.persistErrors.Inc()
			p.log.Warn("store operation failed", "op", j.what, "err", err)
		}
		cancel()
	}
}

// A stored snapshot is the page's tick (u32 LE) followed by the same zlib
// stream of planes that Welcome carries.

func encodeSnapshot(tick uint32, planes []byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, tick), paint.Compress(planes)...)
}

func decodeSnapshot(data []byte) (tick uint32, planes []byte, err error) {
	if len(data) < 4 {
		return 0, nil, errSnapshotShort
	}
	planes, err = paint.Decompress(data[4:], game.GridW*game.GridH*paint.SnapshotBytesPerCell)
	if err != nil {
		return 0, nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return binary.LittleEndian.Uint32(data), planes, nil
}
