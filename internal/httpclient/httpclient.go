// Package httpclient is a shared client with retries, backoff, and timeouts.
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const userAgent = "modeltrack-go/2.0 (+https://github.com/ylnodz/modeltrack-go)"

// RetryableError marks a response worth retrying (429 / 5xx).
type RetryableError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("retryable status %d", e.Status)
}

var shared = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	},
}

// GetJSON performs a GET with bounded exponential backoff + jitter on
// 429/5xx. It honors a numeric Retry-After header when present.
func GetJSON(ctx context.Context, url string, headers map[string]string, params map[string]string) (any, error) {
	const attempts = 3

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		body, status, retryAfter, err := doGet(ctx, url, headers, params)
		if err == nil {
			var out any
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			if derr := dec.Decode(&out); derr != nil {
				return nil, fmt.Errorf("decode %s: %w", url, derr)
			}
			return out, nil
		}

		lastErr = err
		if status == 0 {
			// transport-level failure: always retryable
		} else if status != http.StatusTooManyRequests && status < 500 {
			return nil, err // non-retryable HTTP status
		}
		if attempt == attempts {
			break
		}

		delay := retryAfter
		if delay <= 0 {
			backoff := math.Min(math.Pow(2, float64(attempt))+rand.Float64()*0.5, 15.0)
			delay = time.Duration(backoff * float64(time.Second))
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func doGet(ctx context.Context, url string, headers, params map[string]string) ([]byte, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if len(params) > 0 {
		q := req.URL.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	resp, err := shared.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, 0, 0, err
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After")),
			&RetryableError{Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, 0, fmt.Errorf("http %d for %s", resp.StatusCode, url)
	}
	return body, resp.StatusCode, 0, nil
}

func parseRetryAfter(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(raw); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
