package server

import (
	"fmt"
	"net/http"

	"github.com/VictoriaMetrics/metrics"
)

// hubMetrics are the server's instruments, on their own set so tests and
// multiple hubs in one process do not collide.
type hubMetrics struct {
	set *metrics.Set

	// Pages this instance is simulating.
	pages *metrics.Gauge
	// Time to simulate one page tick and encode its broadcasts.
	tickDuration *metrics.PrometheusHistogram
	// WebSocket payload bytes written; rate() gives bytes per second.
	bytesSent *metrics.Counter
	// State, leaderboard, pong and grid check messages discarded for slow clients.
	statesDropped *metrics.Counter
	// Clients disconnected because they could not keep up with reliable messages.
	clientsKicked *metrics.Counter
	// Fresh paint grids sent to clients whose grid failed a checksum.
	resyncs *metrics.Counter
	// Store writes discarded because the background queue was full.
	persistDropped *metrics.Counter
	// Store operations that failed.
	persistErrors *metrics.Counter
}

func newMetrics() *hubMetrics {
	s := metrics.NewSet()
	s.RegisterMetricsWriter(metrics.WriteProcessMetrics) // go_* and process_*
	return &hubMetrics{
		set:   s,
		pages: s.NewGauge("wetpaint_pages", nil),
		tickDuration: s.NewPrometheusHistogramExt("wetpaint_tick_duration_seconds",
			[]float64{0.0001, 0.00025, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05}),
		bytesSent:      s.NewCounter("wetpaint_bytes_sent_total"),
		statesDropped:  s.NewCounter("wetpaint_droppable_messages_dropped_total"),
		clientsKicked:  s.NewCounter("wetpaint_clients_kicked_total"),
		resyncs:        s.NewCounter("wetpaint_grid_resyncs_total"),
		persistDropped: s.NewCounter("wetpaint_persist_jobs_dropped_total"),
		persistErrors:  s.NewCounter("wetpaint_persist_errors_total"),
	}
}

// Per-page series: wetpaint_players counts connections that have joined,
// alive or dead; wetpaint_spectators those that have not joined yet.
func playersName(page uint32) string { return fmt.Sprintf(`wetpaint_players{page="%d"}`, page) }

func spectatorsName(page uint32) string { return fmt.Sprintf(`wetpaint_spectators{page="%d"}`, page) }

// pageGauges returns the player and spectator gauges of a page, creating
// them if needed.
func (m *hubMetrics) pageGauges(page uint32) (players, spectators *metrics.Gauge) {
	return m.set.GetOrCreateGauge(playersName(page), nil), m.set.GetOrCreateGauge(spectatorsName(page), nil)
}

// dropPage removes a page's series. Call it before the page number can be
// claimed again, or it would remove the new page's series instead.
func (m *hubMetrics) dropPage(page uint32) {
	m.set.UnregisterMetric(playersName(page))
	m.set.UnregisterMetric(spectatorsName(page))
}

// ServeHTTP writes the set in the Prometheus text format.
func (m *hubMetrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	m.set.WritePrometheus(w)
}
