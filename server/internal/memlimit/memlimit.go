// Package memlimit sizes the Go runtime's soft memory limit (GOMEMLIMIT) to
// the memory available to the process, so the garbage collector works harder
// before a container hits its limit and gets OOM-killed.
package memlimit

import (
	"context"
	"log/slog"
	"time"

	"github.com/KimMachineGun/automemlimit/memlimit"
)

// RefreshInterval is how often the limit is re-read, so an in-place resize
// of the container's memory limit takes effect without a restart.
const RefreshInterval = time.Minute

// Set sets GOMEMLIMIT to 90% of the cgroup memory limit, or of system memory
// where there is none (no cgroup limit, or not Linux), and keeps it current
// until ctx is done. GOMEMLIMIT or AUTOMEMLIMIT=off in the environment take
// precedence and disable both. Failures are logged, not returned: the
// runtime then keeps its previous limit, which is no reason to stop.
func Set(ctx context.Context, log *slog.Logger) {
	_, _ = memlimit.Set(
		memlimit.WithLogger(log),
		memlimit.WithProvider(memlimit.ApplyFallback(memlimit.FromCgroup, memlimit.FromSystem)),
		memlimit.WithRefreshInterval(ctx, RefreshInterval),
	)
}
