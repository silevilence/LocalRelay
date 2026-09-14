package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// doStreamRequest uses the client's budget for waiting for response headers and
// for inactivity while reading the body, rather than for the entire generation.
// net/http.Client.Timeout includes Response.Body reads, so even a healthy SSE
// stream is cut off at that total deadline (https://pkg.go.dev/net/http#Client).
// The request context still enforces client cancellation and aggregation limits.
func doStreamRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	if client.Timeout <= 0 {
		return client.Do(req)
	}
	ctx, cancel := context.WithCancelCause(req.Context())
	watch := &streamTimeout{
		ctx: ctx, cancel: cancel, timeout: client.Timeout,
		phase: "upstream response timeout", deadline: time.Now().Add(client.Timeout),
	}
	watch.mu.Lock()
	watch.timer = time.AfterFunc(client.Timeout, watch.expire)
	watch.mu.Unlock()
	// Copy before use; do not mutate the shared client while other requests run.
	// Transport, redirect policy and cookie jar are intentionally shared.
	streamClient := *client
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req.WithContext(ctx))
	if err != nil {
		if cause := watch.finish(); cause != nil {
			err = cause
		}
		return nil, err
	}
	watch.refresh("upstream stream idle timeout")
	// Also bound error response bodies; requesting stream=true does not guarantee
	// the upstream will return a successful SSE response.
	resp.Body = &streamTimeoutBody{ReadCloser: resp.Body, watch: watch}
	return resp, nil
}

type streamTimeout struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelCauseFunc
	timer    *time.Timer
	timeout  time.Duration
	deadline time.Time
	phase    string
	stopped  bool
	finished bool
	cause    error
}

func (s *streamTimeout) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	// Reset can race with an already scheduled AfterFunc callback. Consult the
	// latest deadline under the same lock instead of canceling a refreshed read.
	if remaining := time.Until(s.deadline); remaining > 0 {
		s.timer.Reset(remaining)
		return
	}
	s.stopped = true
	s.cancel(fmt.Errorf("%s after %s: %w", s.phase, s.timeout, context.DeadlineExceeded))
}

func (s *streamTimeout) refresh(phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if phase != "" {
		s.phase = phase
	}
	s.deadline = time.Now().Add(s.timeout)
	s.timer.Reset(s.timeout)
}

// finish releases both the timer and the child context. Capture the cause first
// so our cleanup cancellation cannot turn a normal EOF into "context canceled".
func (s *streamTimeout) finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished {
		s.stopped = true
		s.timer.Stop()
		s.cause = context.Cause(s.ctx)
		s.cancel(nil)
		s.finished = true
	}
	return s.cause
}

type streamTimeoutBody struct {
	io.ReadCloser
	watch *streamTimeout
}

func (b *streamTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		if cause := b.watch.finish(); cause != nil {
			err = cause
		}
	} else if n > 0 {
		// Any bytes count, including SSE comments/heartbeats and partial events.
		b.watch.refresh("")
	}
	return n, err
}

func (b *streamTimeoutBody) Close() error {
	b.watch.finish()
	return b.ReadCloser.Close()
}
