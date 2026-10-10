package request

import (
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultRetryMaxWait is the default cap for server-provided retry delays.
const DefaultRetryMaxWait = 5 * time.Minute

// retryTransport wraps an inner RoundTripper and retries on network errors
// and 5xx responses with exponential backoff + jitter.  4xx responses are
// returned immediately without retrying.
type retryTransport struct {
	inner          http.RoundTripper
	maxRetry       int
	retryUnsafe    bool
	baseDelay      time.Duration
	maxWait        time.Duration
	logger         io.Writer
	onRetryRequest func(*http.Request) error
}

func (rt retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !rt.shouldRetryMethod(req.Method) {
		return rt.inner.RoundTrip(req)
	}
	var (
		resp *http.Response
		err  error
	)

	for attempt := 0; attempt <= rt.maxRetry; attempt++ {
		if attempt > 0 {
			lastResp, lastErr := resp, err
			resp, err = nil, nil
			// Can only retry if we can recreate the body.
			if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
				// Body is not replayable; return whatever we have.
				return lastResp, lastErr
			}

			wait := rt.waitDuration(lastResp, attempt)
			rt.logRetry(attempt, wait)
			timer := time.NewTimer(wait)
			select {
			case <-req.Context().Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, req.Context().Err()
			case <-timer.C:
			}

			// Refresh the request body for the retry.
			if req.GetBody != nil {
				newBody, gbErr := req.GetBody()
				if gbErr != nil {
					return nil, gbErr
				}
				req = req.Clone(req.Context())
				req.Body = newBody
			}
			if rt.onRetryRequest != nil {
				if retryErr := rt.onRetryRequest(req); retryErr != nil {
					return nil, fmt.Errorf("refreshing authentication for retry: %w", retryErr)
				}
			}
		}

		resp, err = rt.inner.RoundTrip(req)

		if err != nil {
			// Network-level error — retry.
			continue
		}

		if shouldRetryStatus(resp.StatusCode) {
			if attempt < rt.maxRetry {
				// Only drain and retry if we can recreate the body (or there is none).
				// If GetBody is nil the body is not replayable; return now with body intact.
				if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
					return resp, nil
				}
				// Drain and close so the connection can be reused, then retry.
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				continue
			}
			// Final attempt — leave body open for the caller.
			return resp, nil
		}

		// Success or 4xx — return as-is.
		return resp, nil
	}

	return resp, err
}

func shouldRetryStatus(status int) bool {
	switch status {
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		http.StatusRequestTimeout,
		http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func (rt retryTransport) shouldRetryMethod(method string) bool {
	if rt.retryUnsafe {
		return true
	}
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead:
		return true
	default:
		return false
	}
}

func (rt retryTransport) logRetry(attempt int, wait time.Duration) {
	if rt.logger == nil {
		return
	}
	fmt.Fprintf(rt.logger, "warning: retry %d/%d in %s\n", attempt, rt.maxRetry, wait.Round(time.Millisecond))
}

func (rt retryTransport) Close() error {
	if closer, ok := rt.inner.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (rt retryTransport) CloseIdleConnections() {
	if closer, ok := rt.inner.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// waitDuration returns the duration to wait before the next attempt.
// It honours the Retry-After response header when present (capped at maxWait);
// otherwise it computes exponential backoff with ±25 % jitter.
func (rt retryTransport) waitDuration(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			// Integer seconds form.
			if secs, parseErr := strconv.Atoi(ra); parseErr == nil && secs >= 0 {
				wait := time.Duration(secs) * time.Second
				return rt.capWait(wait)
			}
			// HTTP-date form.
			if t, parseErr := http.ParseTime(ra); parseErr == nil {
				if wait := time.Until(t); wait > 0 {
					return rt.capWait(wait)
				}
			}
		}
		if retryIn := resp.Header.Get("X-Retry-In"); retryIn != "" {
			if secs, parseErr := strconv.Atoi(retryIn); parseErr == nil && secs >= 0 {
				wait := time.Duration(secs) * time.Second
				return rt.capWait(wait)
			}
		}
	}

	// Exponential backoff: baseDelay * 2^(attempt-1), capped at 30 s.
	base := rt.baseDelay * (1 << uint(attempt-1))
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	// Equal jitter: base/2 + random[0, base). Guard against base < 2 to avoid
	// rand.Int64N(0) panic.
	if int64(base) < 2 {
		return base
	}
	return base/2 + time.Duration(rand.Int64N(int64(base)))
}

func (rt retryTransport) capWait(wait time.Duration) time.Duration {
	maxWait := rt.maxWait
	if maxWait <= 0 {
		maxWait = DefaultRetryMaxWait
	}
	if wait > maxWait {
		return maxWait
	}
	return wait
}
