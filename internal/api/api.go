// Package api exposes the /health endpoint.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/provider"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

// Health is the JSON payload returned by GET /health.
type Health struct {
	Status          string            `json:"status"`
	LastRun         any               `json:"last_run"`
	SecondsSinceRun any               `json:"seconds_since_run"`
	LastSummary     tracker.Summary   `json:"last_summary"`
	Providers       map[string]string `json:"providers"`
	Unconfigured    []string          `json:"unconfigured"`
	ModelsTotal     int               `json:"models_total"`
	ModelsFree      int               `json:"models_free"`
	IntervalMinutes int               `json:"interval_minutes"`
}

// Handler serves the health payload from current tracker + store state.
func Handler(cfg config.Config, store *state.Store, trk *tracker.Tracker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		counts, err := store.Counts(ctx)
		if err != nil {
			http.Error(w, `{"status":"error"}`, http.StatusInternalServerError)
			return
		}
		totalFree, totalAll := 0, 0
		for _, c := range counts {
			totalFree += c.Free
			totalAll += c.Total
		}
		lastRun, summary, status := trk.Snapshot()

		var lastRunVal, sinceVal any
		if !lastRun.IsZero() {
			lastRunVal = float64(lastRun.UnixNano()) / 1e9
			sinceVal = int(time.Since(lastRun).Seconds())
		}

		payload := Health{
			Status:          "healthy",
			LastRun:         lastRunVal,
			SecondsSinceRun: sinceVal,
			LastSummary:     summary,
			Providers:       status,
			Unconfigured:    provider.Unconfigured(cfg),
			ModelsTotal:     totalAll,
			ModelsFree:      totalFree,
			IntervalMinutes: int(cfg.TrackInterval.Minutes()),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	return mux
}

// Serve runs the health server on the configured port until ctx is cancelled.
func Serve(ctx context.Context, cfg config.Config, store *state.Store, trk *tracker.Tracker) error {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           Handler(cfg, store, trk),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
