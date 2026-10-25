// Package web serves the embedded browser UI and its JSON API.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"go-web-side/installer"
)

//go:embed static
var staticFS embed.FS

// Handler returns the HTTP handler for the web UI and API. host is the
// address the server listens on (e.g. "127.0.0.1:5000"); requests that
// arrive for any other Host or from any other Origin are rejected, so web
// pages from elsewhere cannot drive the installer through localhost.
//
// If onIdle is non-nil it is called once the page has been connected and then
// stays gone for idleGrace (the window was closed).
func Handler(host string, onIdle func()) (http.Handler, error) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	sessions := &tracker{grace: idleGrace, onIdle: onIdle}

	var busy sync.Mutex // one install/uninstall at a time
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /api/session", sessions.serve)
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, installer.GetStatus())
	})
	mux.HandleFunc("GET /api/doctor", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, installer.Doctor())
	})
	var picking sync.Mutex // one folder dialog at a time
	mux.HandleFunc("POST /api/browse", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&req) // an empty or bad body just means no starting folder
		if !picking.TryLock() {
			writeJSON(w, http.StatusConflict, browseResult{Error: "A folder dialog is already open."})
			return
		}
		defer picking.Unlock()
		dir, ok, err := installer.PickFolder(req.Path)
		if err != nil {
			writeJSON(w, http.StatusOK, browseResult{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, browseResult{Path: dir, Cancelled: !ok})
	})
	mux.HandleFunc("POST /api/validate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Scope string `json:"scope"`
			Path  string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		scope, err := installer.ParseScope(req.Scope)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, installer.Validation{Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, installer.ValidateDir(scope, req.Path))
	})
	mux.HandleFunc("POST /api/install", streamed(&busy, installer.Install))
	mux.HandleFunc("POST /api/uninstall", streamed(&busy, func(s installer.Scope, _ string, p installer.Progress) installer.Result {
		return installer.Uninstall(s, p)
	}))

	return guard(host, mux), nil
}

// browseResult is the reply to /api/browse.
type browseResult struct {
	Path      string `json:"path,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
	Error     string `json:"error,omitempty"`
}

// idleGrace is how long the page may be disconnected (for example while
// reloading) before it counts as closed.
const idleGrace = 3 * time.Second

// tracker watches the page's long-lived /api/session connection. Unlike a
// timer-based heartbeat it is not slowed down when the window is minimized,
// and the connection drops promptly when the window is closed or killed.
type tracker struct {
	grace  time.Duration
	onIdle func()

	mu    sync.Mutex
	open  int
	timer *time.Timer
}

func (t *tracker) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	t.connect()
	defer t.disconnect()

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (t *tracker) connect() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.open++
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

func (t *tracker) disconnect() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.open--
	if t.open == 0 && t.onIdle != nil {
		t.timer = time.AfterFunc(t.grace, t.onIdle)
	}
}

// event is one line of a streamed response: a step as it happens, then the result.
type event struct {
	Type   string            `json:"type"` // "step" or "result"
	Step   *installer.Step   `json:"step,omitempty"`
	Result *installer.Result `json:"result,omitempty"`
}

// streamed adapts an installer action that takes {"scope": "...", "path": "..."}
// (path optional) to a handler. Once the request is accepted the reply is
// newline-delimited JSON events, flushed as each step happens, so the page can
// show live progress; rejected requests get a single plain JSON result.
func streamed(busy *sync.Mutex, action func(installer.Scope, string, installer.Progress) installer.Result) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Scope string `json:"scope"`
			Path  string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, installer.Result{Message: "Invalid request body."})
			return
		}
		scope, err := installer.ParseScope(req.Scope)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, installer.Result{Message: err.Error()})
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, installer.Result{Message: "Streaming is not supported."})
			return
		}
		if !busy.TryLock() {
			writeJSON(w, http.StatusConflict, installer.Result{Message: "Another operation is already running."})
			return
		}
		defer busy.Unlock()

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		send := func(ev event) {
			enc.Encode(ev)
			flusher.Flush()
		}
		result := action(scope, req.Path, func(s installer.Step) { send(event{Type: "step", Step: &s}) })
		send(event{Type: "result", Result: &result})
	}
}

// guard rejects requests with a foreign Host or Origin (DNS rebinding and
// cross-site requests) and non-JSON POSTs (which also forces a CORS preflight).
func guard(host string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
