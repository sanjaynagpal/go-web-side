package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// connect opens /api/session and returns a func that drops the connection,
// as closing the browser window does.
func connect(t *testing.T, url string) (drop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/session", nil)
	resp, err := http.DefaultClient.Do(req) // returns once headers are flushed
	if err != nil {
		t.Fatal(err)
	}
	return func() { cancel(); resp.Body.Close() }
}

func newServer(t *testing.T, grace time.Duration) (url string, idle chan struct{}) {
	t.Helper()
	idle = make(chan struct{}, 1)
	tr := &tracker{grace: grace, onIdle: func() { idle <- struct{}{} }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", tr.serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, idle
}

func TestIdleAfterWindowCloses(t *testing.T) {
	url, idle := newServer(t, 100*time.Millisecond)
	drop := connect(t, url)

	select {
	case <-idle:
		t.Fatal("idle fired while the window was still connected")
	case <-time.After(300 * time.Millisecond):
	}

	drop()
	select {
	case <-idle:
	case <-time.After(2 * time.Second):
		t.Fatal("idle did not fire after the connection dropped")
	}
}

func TestReloadWithinGraceDoesNotCountAsClosed(t *testing.T) {
	url, idle := newServer(t, 500*time.Millisecond)
	connect(t, url)()       // first page load, then it goes away
	drop := connect(t, url) // reload reconnects within the grace period
	defer drop()

	select {
	case <-idle:
		t.Fatal("idle fired even though the page reconnected")
	case <-time.After(1 * time.Second):
	}
}

func TestNeverConnectedNeverIdle(t *testing.T) {
	_, idle := newServer(t, 50*time.Millisecond)
	select {
	case <-idle:
		t.Fatal("idle fired without any connection")
	case <-time.After(300 * time.Millisecond):
	}
}
