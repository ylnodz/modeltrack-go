package bot

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLogs redirects the default slog logger into a buffer for the duration
// of fn and returns what was written.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

func TestDiagTransportLeavesJSONAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"url":"wss://gateway.discord.gg"}`)
	}))
	defer srv.Close()

	client := &http.Client{Transport: newDiagTransport(nil)}
	logs := captureLogs(t, func() {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != `{"url":"wss://gateway.discord.gg"}` {
			t.Fatalf("body altered: %q", body)
		}
	})
	if strings.Contains(logs, "non-JSON") {
		t.Fatalf("json response should not be reported: %s", logs)
	}
}

// A Cloudflare rate-limit page is the shape that produced the opaque
// "invalid character 'e' looking for beginning of value" in production: a 200
// whose body is plain text. The transport must log the status and body while
// still handing the bytes on so the caller's error is unchanged.
func TestDiagTransportReportsNonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Server", "cloudflare")
		_, _ = io.WriteString(w, "error code: 1015")
	}))
	defer srv.Close()

	client := &http.Client{Transport: newDiagTransport(nil)}
	var body []byte
	logs := captureLogs(t, func() {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ = io.ReadAll(resp.Body)
	})

	if string(body) != "error code: 1015" {
		t.Fatalf("body not preserved for caller: %q", body)
	}
	for _, want := range []string{"non-JSON", "status=200", "error code: 1015", "cloudflare"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %q:\n%s", want, logs)
		}
	}
}

// Non-GET requests are streamed through untouched so uploads are never buffered.
func TestDiagTransportSkipsNonGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()

	client := &http.Client{Transport: newDiagTransport(nil)}
	logs := captureLogs(t, func() {
		resp, err := client.Post(srv.URL, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
	})
	if strings.Contains(logs, "non-JSON") {
		t.Fatalf("POST should not be inspected: %s", logs)
	}
}
