package bot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/model"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

// overrideAPI points discordgo's pre-computed endpoint variables at a stub.
// EndpointChannels/EndpointApplications are concatenated at package init, so
// overriding EndpointAPI alone has no effect on them.
func overrideAPI(t *testing.T, base string) {
	t.Helper()
	prevAPI := discordgo.EndpointAPI
	prevApps := discordgo.EndpointApplications
	prevChans := discordgo.EndpointChannels
	discordgo.EndpointAPI = base + "/api/v10/"
	discordgo.EndpointApplications = base + "/api/v10/applications"
	discordgo.EndpointChannels = base + "/api/v10/channels/"
	t.Cleanup(func() {
		discordgo.EndpointAPI = prevAPI
		discordgo.EndpointApplications = prevApps
		discordgo.EndpointChannels = prevChans
	})
}

// TestConsumeQueueHoldsMessagesUntilReady proves a message posted before the
// gateway handshake is not dropped: it is delivered once READY fires.
func TestConsumeQueueHoldsMessagesUntilReady(t *testing.T) {
	var (
		mu         sync.Mutex
		deliveries []string
		sawMessage = make(chan struct{}, 8)
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/messages") {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			deliveries = append(deliveries, string(body))
			mu.Unlock()
			select {
			case sawMessage <- struct{}{}:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()
	overrideAPI(t, srv.URL)

	store, _ := state.Open(filepath.Join(t.TempDir(), "q.db"))
	defer store.Close()

	cfg := config.Config{DiscordBotToken: "t", DiscordChannelID: "1"}
	trk := tracker.New(cfg, store, nil)

	s, _ := discordgo.New("Bot t")
	b := &Bot{
		Config: cfg, Store: store, Tracker: trk, Session: s,
		queue: make(chan queued, 8), ready: make(chan struct{}),
		pagers: map[string]*pager{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeQueue(ctx)

	ev := state.Event{Kind: state.New, Model: model.Model{Source: "openrouter", PK: "x/y:free", Name: "x/y", IsFree: true}}
	if err := b.sendEvent(ctx, ev, "hello"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-sawMessage:
		t.Fatal("message delivered before READY")
	case <-time.After(400 * time.Millisecond):
		// expected: held while not ready
	}

	b.onReady(s, &discordgo.Ready{User: &discordgo.User{ID: "1", Username: "m"}})

	select {
	case <-sawMessage:
	case <-time.After(3 * time.Second):
		t.Fatal("held message never delivered after READY")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(deliveries))
	}
	var payload discordgo.MessageSend
	if err := json.Unmarshal([]byte(deliveries[0]), &payload); err != nil {
		t.Fatalf("payload not MessageSend JSON: %v (%s)", err, deliveries[0])
	}
	if payload.Content != "hello" {
		t.Fatalf("content: %q", payload.Content)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("embeds: %d", len(payload.Embeds))
	}
}
