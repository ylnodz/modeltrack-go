package bot

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// diagTransport wraps the HTTP client discordgo uses and reports responses
// whose body is not JSON.
//
// discordgo unmarshals REST bodies without checking the content type, so a
// non-JSON response surfaces only as a bare encoding/json error — for example
// "invalid character 'e' looking for beginning of value" from Cloudflare's
// plain-text "error code: 1015" rate-limit page. That message says nothing
// about the status or the body, which makes an upstream block indistinguishable
// from a bug in the bot. This transport logs the status and a short body
// snippet for the failing response, then hands the body on untouched.
type diagTransport struct {
	base http.RoundTripper
}

// jsonSnippetLimit bounds how much of a non-JSON body is logged.
const jsonSnippetLimit = 300

func newDiagTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &diagTransport{base: base}
}

func (t *diagTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	// Only GETs carry no request body and are safe to buffer cheaply; skip
	// anything else so uploads and large payloads are never held in memory.
	if req.Method != http.MethodGet {
		return resp, nil
	}

	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil {
		slog.Error("discord response unreadable", "url", req.URL.Path, "err", readErr)
		return resp, nil
	}
	if len(body) == 0 || json.Valid(body) {
		return resp, nil
	}

	slog.Error("discord returned a non-JSON response",
		"url", req.URL.Path,
		"status", resp.StatusCode,
		"content_type", resp.Header.Get("Content-Type"),
		"server", resp.Header.Get("Server"),
		"cf_ray", resp.Header.Get("CF-Ray"),
		"body", snippet(body),
	)
	return resp, nil
}

// snippet collapses a body to one line and truncates it for logging.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > jsonSnippetLimit {
		s = s[:jsonSnippetLimit] + "…"
	}
	return s
}
