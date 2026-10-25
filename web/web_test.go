package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-web-side/installer"
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

func TestStreamedSendsStepsThenResult(t *testing.T) {
	var busy sync.Mutex
	h := streamed(&busy, func(s installer.Scope, path string, p installer.Progress) installer.Result {
		p(installer.Step{Name: "Copy", Status: "run"})
		p(installer.Step{Name: "Copy", Status: "ok", Detail: path})
		return installer.Result{OK: true, Message: "done"}
	})

	req := httptest.NewRequest("POST", "/api/install", strings.NewReader(`{"scope":"user","path":"X:\\app"}`))
	rec := httptest.NewRecorder()
	h(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type = %q", ct)
	}
	var got []event
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		var ev event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		got = append(got, ev)
	}
	if len(got) != 3 || got[0].Type != "step" || got[0].Step.Status != "run" ||
		got[1].Step.Status != "ok" || got[1].Step.Detail != `X:\app` ||
		got[2].Type != "result" || !got[2].Result.OK {
		t.Fatalf("unexpected events: %+v", got)
	}
}

func TestStreamedRejectsBadRequestsWithPlainJSON(t *testing.T) {
	var busy sync.Mutex
	h := streamed(&busy, func(installer.Scope, string, installer.Progress) installer.Result {
		t.Fatal("action must not run")
		return installer.Result{}
	})
	for _, body := range []string{`not json`, `{"scope":"nope"}`} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("POST", "/api/install", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("body %q: status %d, type %q", body, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestStreamedRefusesConcurrentRuns(t *testing.T) {
	var busy sync.Mutex
	busy.Lock()
	h := streamed(&busy, func(installer.Scope, string, installer.Progress) installer.Result { return installer.Result{} })
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/api/install", strings.NewReader(`{"scope":"user"}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}
