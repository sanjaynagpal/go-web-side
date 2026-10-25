//go:build windows

package installer

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// fakeWorker stands in for the elevated child: it appends JSON lines to the
// result file named after "-result" the way RunWorker does, with pauses so the
// parent has to follow the file while it grows.
func fakeWorker(t *testing.T, lines func(write func(string))) func([]string) error {
	return func(args []string) error {
		var path string
		for i, a := range args {
			if a == "-result" {
				path = args[i+1]
			}
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Error(err)
			return err
		}
		defer f.Close()
		lines(func(s string) { f.WriteString(s); time.Sleep(250 * time.Millisecond) })
		return nil
	}
}

func line(v any) string {
	b, _ := json.Marshal(v)
	return string(b) + "\n"
}

func TestElevatedProgressIsFollowedLive(t *testing.T) {
	if isElevated() {
		t.Skip("running elevated, so no helper would be launched")
	}
	defer func(old func([]string) error) { elevate = old }(elevate)

	running := Step{Name: "Copy", Status: "run"}
	done := Step{Name: "Copy", Status: "ok", Detail: "1 byte"}
	final := Result{OK: true, Message: "all done", Steps: []Step{done}}
	whole := line(event{Result: &final})

	elevate = fakeWorker(t, func(write func(string)) {
		write(line(event{Step: &running}))
		write(line(event{Step: &done})[:10]) // a line still being written...
		write(line(event{Step: &done})[10:]) // ...completed on the next poll
		write(whole)
	})

	var got []Step
	var times []time.Time
	r := withElevation(ScopeMachine, "install", nil, func(s Step) { got = append(got, s); times = append(times, time.Now()) },
		func(Scope, Progress) Result { t.Fatal("must run through the helper, not in-process"); return Result{} })

	if !r.OK || r.Message != "all done" {
		t.Fatalf("result = %+v", r)
	}
	// The half-written line must not be lost or delivered twice. (It is delivered
	// once, when complete; the unfinished text is never parsed on its own.)
	if len(got) != 2 || got[0] != running || got[1].Status != "ok" {
		t.Fatalf("steps = %+v, want [run, ok]", got)
	}
	if gap := times[1].Sub(times[0]); gap < 200*time.Millisecond {
		t.Errorf("steps arrived %v apart; they should be delivered as they are written, not all at the end", gap)
	}
}

func TestElevationDeclinedAndNoResult(t *testing.T) {
	if isElevated() {
		t.Skip("running elevated, so no helper would be launched")
	}
	defer func(old func([]string) error) { elevate = old }(elevate)
	never := func(Scope, Progress) Result { return Result{} }

	elevate = func([]string) error { return errElevationDeclined }
	if r := withElevation(ScopeMachine, "install", nil, nil, never); r.OK || r.Code != CodeElevationDeclined {
		t.Errorf("declined: %+v", r)
	}

	elevate = func([]string) error { return nil } // exits without reporting anything
	if r := withElevation(ScopeMachine, "install", nil, nil, never); r.OK || r.Message == "" {
		t.Errorf("no result: %+v", r)
	}
}
