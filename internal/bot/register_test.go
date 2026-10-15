package bot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

// TestOnReadyRegistersCommandsViaRealAPIPath drives the actual onReady handler
// against a stub Discord API, proving commands are registered after READY and
// that the request carries the full provider choice list. This is the path that
// segfaulted in production (registration ran before State.User existed).
func TestOnReadyRegistersCommandsViaRealAPIPath(t *testing.T) {
	type captured struct {
		method string
		path   string
		body   []*discordgo.ApplicationCommand
	}

	var (
		mu   sync.Mutex
		got  []captured
		srv  *httptest.Server
		done = make(chan struct{}, 1)
	)

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []*discordgo.ApplicationCommand
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, captured{method: r.Method, path: r.URL.Path, body: body})
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	// Point discordgo at the stub instead of discord.com.
	overrideAPI(t, srv.URL)

	store, err := state.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.Config{DiscordBotToken: "token", DiscordChannelID: "1"}
	trk := tracker.New(cfg, store, nil)

	s, err := discordgo.New("Bot " + cfg.DiscordBotToken)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{Config: cfg, Store: store, Tracker: trk, Session: s, pagers: map[string]*pager{}}

	// Precondition of the production crash: no user in session state yet.
	if s.State != nil && s.State.User != nil {
		t.Fatal("precondition: State.User must be nil before READY")
	}

	// Fire the READY event exactly as the gateway would.
	b.onReady(s, &discordgo.Ready{User: &discordgo.User{ID: "424242", Username: "modeltrack"}})

	select {
	case <-done:
	default:
		t.Fatal("READY did not trigger a command registration request")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("want 1 registration request, got %d", len(got))
	}
	req := got[0]
	// Global registration: PUT /applications/{appID}/commands
	if req.method != http.MethodPut {
		t.Fatalf("method: %s", req.method)
	}
	if !strings.Contains(req.path, "/applications/424242/commands") {
		t.Fatalf("path: %s", req.path)
	}
	if len(req.body) != 2 {
		t.Fatalf("want 2 commands, got %d", len(req.body))
	}
	if req.body[0].Name != "free" || req.body[1].Name != "status" {
		t.Fatalf("names: %s/%s", req.body[0].Name, req.body[1].Name)
	}
	if n := len(req.body[0].Options[0].Choices); n != 14 {
		t.Fatalf("want 14 provider choices, got %d", n)
	}
}

// TestOnReadyGuildScopedClearsGlobalScope covers the DISCORD_GUILD_ID path.
// Discord stores guild and global commands independently and renders a command
// once per scope it exists in, so an old global /free left by a previous
// deployment shows up as a duplicate. Guild registration must therefore also
// clear the global scope.
func TestOnReadyGuildScopedClearsGlobalScope(t *testing.T) {
	type captured struct {
		method string
		path   string
		body   []*discordgo.ApplicationCommand
	}
	var (
		mu  sync.Mutex
		got []captured
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []*discordgo.ApplicationCommand
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, captured{method: r.Method, path: r.URL.Path, body: body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	overrideAPI(t, srv.URL)

	store, _ := state.Open(filepath.Join(t.TempDir(), "g.db"))
	defer store.Close()

	cfg := config.Config{DiscordBotToken: "t", DiscordChannelID: "1", DiscordGuildID: "777"}
	trk := tracker.New(cfg, store, nil)
	s, _ := discordgo.New("Bot t")
	b := &Bot{Config: cfg, Store: store, Tracker: trk, Session: s, pagers: map[string]*pager{}}

	b.onReady(s, &discordgo.Ready{User: &discordgo.User{ID: "424242", Username: "m"}})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("want 2 requests (clear globals + set guild), got %d: %+v", len(got), got)
	}

	var cleared, registered *captured
	for i := range got {
		switch {
		case strings.Contains(got[i].path, "/guilds/777/commands"):
			registered = &got[i]
		case strings.HasSuffix(got[i].path, "/applications/424242/commands"):
			cleared = &got[i]
		}
	}
	if cleared == nil {
		t.Fatalf("global scope never cleared: %+v", got)
	}
	if len(cleared.body) != 0 {
		t.Fatalf("global clear must send an empty list, got %d entries", len(cleared.body))
	}
	if registered == nil {
		t.Fatalf("guild scope never registered: %+v", got)
	}
	if len(registered.body) != 2 {
		t.Fatalf("guild registration: want 2 commands, got %d", len(registered.body))
	}
}

// TestOnReadyNilUserDoesNotPanic covers a malformed READY event.
func TestOnReadyNilUserDoesNotPanic(t *testing.T) {
	store, _ := state.Open(filepath.Join(t.TempDir(), "n.db"))
	defer store.Close()
	cfg := config.Config{DiscordBotToken: "t", DiscordChannelID: "1"}
	trk := tracker.New(cfg, store, nil)
	s, _ := discordgo.New("Bot t")
	b := &Bot{Config: cfg, Store: store, Tracker: trk, Session: s, pagers: map[string]*pager{}}

	b.onReady(s, &discordgo.Ready{}) // must not panic
}
