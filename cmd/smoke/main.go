// Command smoke runs a live end-to-end check: real network fetch through the
// tracker, real SQLite, real /health handler. Needs network access.
//
//	go run ./cmd/smoke
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/ylnodz/modeltrack-go/internal/api"
	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "smoke failed:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	tmp, err := os.MkdirTemp("", "modeltrack-smoke")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	cfg := config.Load()
	cfg.DBPath = filepath.Join(tmp, "smoke.db")

	store, err := state.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	sent := 0
	trk := tracker.New(cfg, store, func(context.Context, state.Event, string) error {
		sent++
		return nil
	})

	// cycle 1 — fresh DB, everything is new ⇒ seeded silently (no spam)
	s1 := trk.RunCycle(ctx)
	fmt.Printf("cycle1 summary: %+v\n", s1)
	fmt.Printf("cycle1 announced: %d (expected 0 — fresh-DB seed)\n", sent)

	counts, _ := store.Counts(ctx)
	fmt.Printf("counts: ")
	for k, v := range counts {
		fmt.Printf("%s=(%d,%d) ", k, v.Total, v.Free)
	}
	fmt.Println()

	free, _ := store.FreeModels(ctx, "openrouter")
	fmt.Printf("openrouter free models: %d\n", len(free))
	for i, m := range free {
		if i >= 6 {
			break
		}
		fmt.Printf("   %s  ctx=%s  in=%s out=%s\n", m.PK, ctxText(m.Context), money(m.PriceIn), money(m.PriceOut))
	}

	// cycle 2 — unchanged ⇒ no events
	sent = 0
	s2 := trk.RunCycle(ctx)
	fmt.Printf("cycle2 summary: %+v announced: %d (expected 0 — unchanged)\n", s2, sent)

	// cycle 3 — forget one model ⇒ it must re-announce exactly once
	if len(free) > 0 {
		victim := free[0].PK
		if err := forget(ctx, store, "openrouter", victim); err != nil {
			return err
		}
		fmt.Printf("deleted %s to simulate a brand-new model\n", victim)
		sent = 0
		s3 := trk.RunCycle(ctx)
		fmt.Printf("cycle3 summary: %+v announced: %d (expected 1)\n", s3, sent)
	}

	// health handler
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	api.Handler(cfg, store, trk).ServeHTTP(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		return err
	}
	fmt.Printf("health: %d\n", rec.Code)
	fmt.Printf("  providers: %v\n", payload["providers"])
	fmt.Printf("  models_total: %v models_free: %v\n", payload["models_total"], payload["models_free"])
	fmt.Printf("  interval_minutes: %v\n", payload["interval_minutes"])
	return nil
}

func forget(ctx context.Context, store *state.Store, source, pk string) error {
	// Remove the row so the next cycle treats it as brand-new.
	return store.Delete(ctx, source, pk)
}

func ctxText(v *int) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprint(*v)
}

func money(v *float64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("%.4f", *v)
}

var _ = time.Second
