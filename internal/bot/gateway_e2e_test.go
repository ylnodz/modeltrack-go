package bot

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/ylnodz/modeltrack-go/internal/config"
	"github.com/ylnodz/modeltrack-go/internal/state"
	"github.com/ylnodz/modeltrack-go/internal/tracker"
)

// This reproduces the production startup failure end to end.
//
// The observed error is "open gateway: invalid character 'e' looking for
// beginning of value" with NO "json unmarshal:" prefix, which pins it to the
// websocket message decode in discordgo's onEvent — the REST gateway call wraps
// its errors with that prefix. So an upstream proxy completed the websocket
// upgrade and then delivered a non-JSON payload; Cloudflare's plain-text
// "error code: 1015" block page is the shape that produces a body starting
// with 'e'.
//
// The fake gateway below does exactly that: it answers the gateway REST call
// with a websocket URL, completes the upgrade, then writes the block page as a
// websocket text frame.
func TestOpenRetriesWhenGatewaySendsNonJSONFrame(t *testing.T) {
	wsAddr, hits := startBlockingGateway(t)

	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"ws://` + wsAddr + `"}`))
	}))
	defer restSrv.Close()

	prevGateway := discordgo.EndpointGateway
	discordgo.EndpointGateway = restSrv.URL + "/gateway"
	t.Cleanup(func() { discordgo.EndpointGateway = prevGateway })

	store, err := state.Open(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := config.Config{DiscordBotToken: "x.y.z", DiscordChannelID: "1"}
	b, err := New(cfg, store, tracker.New(cfg, store, nil))
	if err != nil {
		t.Fatal(err)
	}

	prevDelay, prevMax := openBaseDelay, openMaxDelay
	openBaseDelay, openMaxDelay = time.Millisecond, 2*time.Millisecond
	t.Cleanup(func() { openBaseDelay, openMaxDelay = prevDelay, prevMax })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	done := make(chan error, 1)
	go func() { done <- b.openWithRetry(ctx) }()

	// The bug was that this exited fatally. Assert it is still retrying.
	select {
	case err := <-done:
		t.Fatalf("openWithRetry exited after %d gateway hits: %v (want continued retries)", hits.Load(), err)
	case <-time.After(300 * time.Millisecond):
	}

	// Stop the loop and wait for it to actually return before reading the log
	// buffer or restoring globals: the goroutine writes both.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("openWithRetry did not return after cancel")
	}

	if n := hits.Load(); n < 2 {
		t.Fatalf("gateway hits = %d, want repeated retries", n)
	}

	// The underlying json error must still surface, so existing log greps work.
	if !strings.Contains(buf.String(), "looking for beginning of value") {
		t.Fatalf("expected the decode error to be logged; got:\n%s", buf.String())
	}
}
