package otlp

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// slowRoundTripper wraps an http.RoundTripper and delays the first read of each
// response body by the configured duration. This simulates an asymmetric network
// client that is slow to drain responses, holding connections open on the server.
type slowRoundTripper struct {
	wrapped http.RoundTripper
	delay   time.Duration
}

func (t *slowRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.wrapped.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	resp.Body = &slowReadCloser{ReadCloser: resp.Body, delay: t.delay}
	return resp, nil
}

// slowReadCloser delays the first Read call by the configured duration, then
// delegates all subsequent reads to the underlying ReadCloser.
type slowReadCloser struct {
	io.ReadCloser
	delay time.Duration
	once  sync.Once
}

func (s *slowReadCloser) Read(p []byte) (int, error) {
	s.once.Do(func() { time.Sleep(s.delay) })
	return s.ReadCloser.Read(p)
}
