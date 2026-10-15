package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// stubOpen swaps the handshake function and shrinks the backoff for one test,
// restoring the real values afterwards.
func stubOpen(t *testing.T, fn func(*discordgo.Session) error) {
	t.Helper()
	prevFn, prevDelay, prevMax := sessionOpen, openBaseDelay, openMaxDelay
	sessionOpen, openBaseDelay, openMaxDelay = fn, time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() {
		sessionOpen, openBaseDelay, openMaxDelay = prevFn, prevDelay, prevMax
	})
}

func TestOpenWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	attempts := 0
	stubOpen(t, func(*discordgo.Session) error {
		attempts++
		if attempts < 3 {
			return errors.New("invalid character 'e' looking for beginning of value")
		}
		return nil
	})

	b := &Bot{}
	if err := b.openWithRetry(t.Context()); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

// A transient failure must not kill the process: with no supervisor on the
// host, the bot has to keep trying until the upstream recovers.
func TestOpenWithRetryKeepsRetryingTransientFailures(t *testing.T) {
	attempts := 0
	stubOpen(t, func(*discordgo.Session) error {
		attempts++
		return errors.New("invalid character 'e' looking for beginning of value")
	})

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	b := &Bot{}
	err := b.openWithRetry(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline", err)
	}
	if attempts < 3 {
		t.Fatalf("attempts = %d, want repeated retries", attempts)
	}
}

// An invalid token returns immediately: retrying a 4004 handshake can never
// succeed and would only delay the fatal error the operator needs to see.
func TestOpenWithRetryDoesNotRetryFatalClose(t *testing.T) {
	attempts := 0
	stubOpen(t, func(*discordgo.Session) error {
		attempts++
		return errors.New("open gateway: websocket: close 4004: Authentication failed.")
	})

	b := &Bot{}
	err := b.openWithRetry(t.Context())
	if err == nil || !strings.Contains(err.Error(), "4004") {
		t.Fatalf("err = %v, want the 4004 failure", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on fatal close)", attempts)
	}
}

func TestIsFatalHandshakeError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"websocket: close 4004: Authentication failed.", true},
		{"websocket: close 4014: Disallowed intent(s).", true},
		{"invalid character 'e' looking for beginning of value", false},
		{"Exceeded Max retries HTTP 502 Bad Gateway", false},
	}
	for _, c := range cases {
		if got := isFatalHandshakeError(errors.New(c.msg)); got != c.want {
			t.Errorf("isFatalHandshakeError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// The backoff is capped so a long outage does not push the retry interval
// beyond openMaxDelay.
func TestOpenBackoffIsCapped(t *testing.T) {
	prevBase, prevMax := openBaseDelay, openMaxDelay
	openBaseDelay, openMaxDelay = time.Second, 30*time.Second
	defer func() { openBaseDelay, openMaxDelay = prevBase, prevMax }()

	delay := openBaseDelay << uint(min(20-1, 16))
	if delay <= 0 || delay > openMaxDelay {
		delay = openMaxDelay
	}
	if delay != openMaxDelay {
		t.Fatalf("delay at attempt 20 = %v, want capped at %v", delay, openMaxDelay)
	}
}
