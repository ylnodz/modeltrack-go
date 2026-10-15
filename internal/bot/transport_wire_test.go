package bot

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

// The transport that reports non-JSON Discord responses is useless if it is not
// actually installed on the session's HTTP client, so assert the wiring.
func TestNewWiresDiagTransport(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "wire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.Config{DiscordBotToken: "x", DiscordChannelID: "1"}
	trk := tracker.New(cfg, store, nil)

	b, err := New(cfg, store, trk)
	if err != nil {
		t.Fatal(err)
	}
	if b.Session == nil || b.Session.Client == nil {
		t.Fatal("session client not configured")
	}
	if _, ok := b.Session.Client.Transport.(*diagTransport); !ok {
		t.Fatalf("transport = %T, want *diagTransport", b.Session.Client.Transport)
	}
	if b.Session.Identify.Intents == 0 {
		t.Fatal("gateway intents not set")
	}
}

// The wrapper must preserve the client's existing settings (discordgo installs
// a 20s timeout) and the underlying transport, guarding against a future edit
// that replaces the client wholesale.
func TestDiagTransportPreservesClientSettings(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "wire2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.Config{DiscordBotToken: "x", DiscordChannelID: "1"}
	trk := tracker.New(cfg, store, nil)

	b, err := New(cfg, store, trk)
	if err != nil {
		t.Fatal(err)
	}
	if b.Session.Client.Timeout == 0 {
		t.Fatal("discordgo's client timeout was lost")
	}
	dt, ok := b.Session.Client.Transport.(*diagTransport)
	if !ok {
		t.Fatalf("transport = %T, want *diagTransport", b.Session.Client.Transport)
	}
	if dt.base != http.DefaultTransport {
		t.Fatalf("base transport = %T, want http.DefaultTransport", dt.base)
	}
}
