// Package tracker orchestrates a cycle: fetch → diff → notify.
package tracker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/model"
	"github.com/ylnodz/modeltrack-go/internal/provider"
	"github.com/ylnodz/modeltrack-go/internal/state"
)

// Sender delivers one rendered event. It is swapped in by the bot once ready;
// until then events are dropped.
type Sender func(ctx context.Context, e state.Event, content string) error

// Summary is the result of one tracking cycle.
type Summary struct {
	OK        []string `json:"ok"`
	Failed    []string `json:"failed"`
	Models    int      `json:"models"`
	Events    int      `json:"events"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// Tracker runs fetch → diff → notify cycles.
type Tracker struct {
	Config   config.Config
	Store    *state.Store
	Provider []provider.Provider
	Sender   Sender

	mu             sync.Mutex
	LastRun        time.Time
	LastSummary    Summary
	ProviderStatus map[string]string
}

// New builds a tracker wired to every known provider.
func New(cfg config.Config, store *state.Store, sender Sender) *Tracker {
	return &Tracker{
		Config:         cfg,
		Store:          store,
		Provider:       provider.Build(cfg),
		Sender:         sender,
		ProviderStatus: map[string]string{},
	}
}

// RunCycle fetches every configured provider concurrently, diffs the result
// against stored state, and emits the events that warrant an announcement.
func (t *Tracker) RunCycle(ctx context.Context) Summary {
	started := time.Now()

	// Seed the first-ever run silently rather than spamming an empty channel.
	seeded, _ := t.Store.GetMeta(ctx, "seeded")
	seeding := seeded == ""

	type fetchedResult struct {
		key    string
		models []model.Model
		err    error
	}

	var wg sync.WaitGroup
	resultsCh := make(chan fetchedResult, len(t.Provider))

	for _, p := range t.Provider {
		if !p.Configured() {
			t.ProviderStatus[p.Key()] = "unconfigured"
			continue
		}
		wg.Add(1)
		go func(p provider.Provider) {
			defer wg.Done()
			// Bounded per provider so one slow API cannot stall the cycle.
			cctx, cancel := context.WithTimeout(ctx, t.Config.ProviderTimeout)
			defer cancel()

			models, err := p.Fetch(cctx)
			resultsCh <- fetchedResult{key: p.Key(), models: models, err: err}
		}(p)
	}
	wg.Wait()
	close(resultsCh)

	fetched := map[string][]model.Model{}
	var ok, failed []string
	for r := range resultsCh {
		if r.err != nil {
			failed = append(failed, r.key)
			t.ProviderStatus[r.key] = "error: " + errName(r.err)
			slog.Warn("provider failed", "key", r.key, "err", r.err)
			continue
		}
		fetched[r.key] = r.models
		ok = append(ok, r.key)
		t.ProviderStatus[r.key] = fmt.Sprintf("ok (%d)", len(r.models))
	}

	watchPrice := map[string]bool{}
	watchContext := map[string]bool{}
	for _, p := range t.Provider {
		if p.WatchPrice() {
			watchPrice[p.Key()] = true
		}
		if p.WatchContext() {
			watchContext[p.Key()] = true
		}
	}

	events, err := t.Store.DiffAndApply(ctx, fetched, watchPrice, watchContext)
	if err != nil {
		slog.Error("diff failed", "err", err)
	}

	if seeding {
		events = nil
	}
	_ = t.Store.SetMeta(ctx, "seeded", "1")

	byKey := map[string]provider.Provider{}
	for _, p := range t.Provider {
		byKey[p.Key()] = p
	}

	emitted := 0
	for _, e := range events {
		if !wanted(byKey, e) {
			continue
		}
		if err := t.emit(ctx, e); err != nil {
			slog.Warn("emit failed", "kind", e.Kind, "pk", e.Model.PK, "err", err)
			continue
		}
		emitted++
		if err := t.Store.RecordEvent(ctx, e); err != nil {
			slog.Warn("record event failed", "err", err)
		}
	}

	total := 0
	for _, ms := range fetched {
		total += len(ms)
	}
	summary := Summary{
		OK:        ok,
		Failed:    failed,
		Models:    total,
		Events:    emitted,
		ElapsedMS: time.Since(started).Milliseconds(),
	}

	t.mu.Lock()
	t.LastRun = time.Now()
	t.LastSummary = summary
	t.mu.Unlock()

	slog.Info("cycle done", "ok", len(ok), "failed", len(failed),
		"models", total, "events", emitted, "elapsed_ms", summary.ElapsedMS)
	return summary
}

// wanted filters out events a free-only provider should not announce.
func wanted(byKey map[string]provider.Provider, e state.Event) bool {
	p, ok := byKey[e.Model.Source]
	if ok && p.FilterFree() && !e.Model.IsFree {
		return false
	}
	return true
}

func (t *Tracker) emit(ctx context.Context, e state.Event) error {
	content := ""
	if e.Kind == state.BecameFree && t.Config.AnnounceMention {
		content = "@here"
	}
	if t.Sender == nil {
		return nil
	}
	return t.Sender(ctx, e, content)
}

// Snapshot returns the last-run state safely for the health endpoint.
func (t *Tracker) Snapshot() (time.Time, Summary, map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := make(map[string]string, len(t.ProviderStatus))
	for k, v := range t.ProviderStatus {
		status[k] = v
	}
	return t.LastRun, t.LastSummary, status
}

func errName(err error) string {
	if err == nil {
		return ""
	}
	if te, ok := err.(interface{ Timeout() bool }); ok && te.Timeout() {
		return "Timeout"
	}
	return fmt.Sprintf("%T", err)
}
