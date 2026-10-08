package tracker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/model"
	"github.com/ylnodz/modeltrack-go/internal/provider"
	"github.com/ylnodz/modeltrack-go/internal/state"
)

// fakeProvider returns a fixed catalog.
type fakeProvider struct {
	provider.Base
	catalog []model.Model
	err     error
}

func (f *fakeProvider) Fetch(context.Context) ([]model.Model, error) {
	return f.catalog, f.err
}

func newFake(catalog []model.Model) *fakeProvider {
	return &fakeProvider{Base: provider.Base{KeyName: "fake", LabelName: "Fake", Filter: true}, catalog: catalog}
}

func mk(pk string, free bool) model.Model {
	return model.Model{Source: "fake", PK: pk, Name: pk, IsFree: free}
}

func newTracker(t *testing.T, providers []provider.Provider) (*Tracker, *state.Store, *[]state.Event) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sent := &[]state.Event{}
	cfg := config.Config{
		TrackInterval:   time.Minute,
		ProviderTimeout: 5 * time.Second,
	}
	trk := &Tracker{
		Config:         cfg,
		Store:          store,
		Provider:       providers,
		ProviderStatus: map[string]string{},
		Sender: func(_ context.Context, e state.Event, _ string) error {
			*sent = append(*sent, e)
			return nil
		},
	}
	return trk, store, sent
}

func TestFirstCycleSeedsSilently(t *testing.T) {
	trk, store, sent := newTracker(t, []provider.Provider{newFake([]model.Model{mk("a", true)})})
	trk.RunCycle(context.Background())

	if len(*sent) != 0 {
		t.Fatalf("first cycle announced %d events (want 0)", len(*sent))
	}
	all, _ := store.AllModels(context.Background(), "")
	if len(all) != 1 {
		t.Fatalf("want 1 stored model, got %d", len(all))
	}
}

func TestLaterCycleAnnouncesNew(t *testing.T) {
	ctx := context.Background()
	f := newFake([]model.Model{mk("a", true)})
	trk, _, sent := newTracker(t, []provider.Provider{f})
	trk.RunCycle(ctx) // seed

	f.catalog = []model.Model{mk("a", true), mk("b", true)}
	trk.RunCycle(ctx)

	if len(*sent) != 1 || (*sent)[0].Model.PK != "b" {
		t.Fatalf("want one new event for b, got %+v", *sent)
	}
}

func TestFilterFreeSuppressesPaidAnnouncements(t *testing.T) {
	ctx := context.Background()
	f := newFake([]model.Model{mk("a", true)})
	trk, store, sent := newTracker(t, []provider.Provider{f})
	trk.RunCycle(ctx) // seed, filter_free provider

	f.catalog = []model.Model{mk("a", true), mk("paid", false)}
	trk.RunCycle(ctx)

	if len(*sent) != 0 {
		t.Fatalf("filter_free provider announced a paid model: %+v", *sent)
	}
	all, _ := store.AllModels(ctx, "")
	if len(all) != 2 {
		t.Fatalf("paid model should still be recorded, got %d rows", len(all))
	}
}

// boomProvider fails its fetch.
type boomProvider struct{ provider.Base }

func (b *boomProvider) Fetch(context.Context) ([]model.Model, error) {
	return nil, errTest
}

var errTest = &testErr{}

type testErr struct{}

func (e *testErr) Error() string { return "provider exploded" }

func TestOneFailingProviderDoesNotKillCycle(t *testing.T) {
	ok := newFake([]model.Model{mk("a", true)})
	boom := &boomProvider{Base: provider.Base{KeyName: "boom", LabelName: "Boom"}}

	trk, _, _ := newTracker(t, []provider.Provider{ok, boom})
	summary := trk.RunCycle(context.Background())

	if len(summary.OK) != 1 || summary.OK[0] != "fake" {
		t.Fatalf("ok=%v", summary.OK)
	}
	if len(summary.Failed) != 1 || summary.Failed[0] != "boom" {
		t.Fatalf("failed=%v", summary.Failed)
	}
	if summary.Models != 1 {
		t.Fatalf("models=%d", summary.Models)
	}
}
