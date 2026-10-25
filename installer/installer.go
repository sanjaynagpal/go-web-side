// Package installer is a mock installer for the S-Lab desktop application. It
// copies the embedded executable to disk, records the installation in the
// Windows registry, and can later uninstall or diagnose it from that record.
package installer

//go:generate go build -o payload/slab.exe ../cmd/slab

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// exeName is the application executable the installer carries and installs.
const exeName = "slab.exe"

// Version is the version of S-Lab carried in this installer.
const Version = "1.0.0"

//go:embed payload/slab.exe
var payload []byte

// Scope says who an installation is for.
type Scope string

const (
	ScopeUser    Scope = "user"    // current user only; no elevation needed
	ScopeMachine Scope = "machine" // all users; needs elevation
)

// ParseScope validates a scope string.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeUser, ScopeMachine:
		return Scope(s), nil
	}
	return "", fmt.Errorf("unknown scope %q (want %q or %q)", s, ScopeUser, ScopeMachine)
}

// Step is one line of progress or diagnostic output.
type Step struct {
	Name   string `json:"name"`
	Status string `json:"status"` // run (started), ok, warn, fail or info
	Detail string `json:"detail,omitempty"`
	Group  string `json:"group,omitempty"` // heading to list the step under (Doctor)
}

// Progress receives each step of an install or uninstall as it happens: first
// with Status "run" when it starts, then again with its final status.
type Progress func(Step)

// StepDelay is how long each install/uninstall step pretends to take. The real
// work is nearly instant, so this lets a person see the progress. Zero disables it.
var StepDelay = 350 * time.Millisecond

// reporter collects a run's finished steps and forwards each one to progress.
type reporter struct {
	steps    []Step
	progress Progress
}

func (r *reporter) begin(name string) {
	if r.progress != nil {
		r.progress(Step{Name: name, Status: "run"})
	}
	time.Sleep(StepDelay)
}

func (r *reporter) end(name, status, detail string) {
	s := Step{Name: name, Status: status, Detail: detail}
	r.steps = append(r.steps, s)
	if r.progress != nil {
		r.progress(s)
	}
}

// event is one line of the file an elevated worker reports through.
type event struct {
	Step   *Step   `json:"step,omitempty"`
	Result *Result `json:"result,omitempty"`
}

// Result is the outcome of an install, uninstall or doctor run.
type Result struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Steps   []Step `json:"steps,omitempty"`
}

// Info is what the registry records about an installation.
type Info struct {
	Scope       Scope  `json:"scope"`
	Version     string `json:"version"`
	InstallPath string `json:"installPath"`
	InstallDate string `json:"installDate"`
}

// Status summarises the machine for the UI.
type Status struct {
	Supported bool   `json:"supported"`
	Elevated  bool   `json:"elevated"`
	Version   string `json:"version"`
	User      *Info  `json:"user"`
	Machine   *Info  `json:"machine"`

	// DefaultDirs is where each scope installs unless told otherwise.
	DefaultDirs map[Scope]string `json:"defaultDirs"`
}

var errElevationDeclined = errors.New("elevation was declined")

// elevate relaunches this program elevated with args and waits for it. A
// variable so tests can stand in for the UAC prompt.
var elevate = runElevated

// GetStatus reports which installations exist.
func GetStatus() Status {
	return Status{
		Supported: supported,
		Elevated:  isElevated(),
		Version:   Version,
		User:      detect(ScopeUser),
		Machine:   detect(ScopeMachine),
		DefaultDirs: map[Scope]string{
			ScopeUser:    defaultDir(ScopeUser),
			ScopeMachine: defaultDir(ScopeMachine),
		},
	}
}

// ResolveDir returns the folder to install into: the scope's default when dir
// is empty, otherwise dir cleaned and checked. It rejects anything that is not
// a full local path to a folder, so mistakes are reported before any elevation.
func ResolveDir(scope Scope, dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return defaultDir(scope), nil
	}
	dir = filepath.Clean(dir)
	switch {
	case strings.ContainsAny(dir[len(filepath.VolumeName(dir)):], `<>:"|?*`):
		return "", errors.New(`the install folder contains a character Windows does not allow (< > : " | ? *)`)
	case !filepath.IsAbs(dir):
		return "", fmt.Errorf(`the install folder must be a full path such as C:\Apps\S-Lab (got %q)`, dir)
	case strings.HasPrefix(dir, `\\`):
		return "", errors.New("network folders are not supported; choose a folder on this computer")
	case filepath.Dir(dir) == dir:
		return "", fmt.Errorf("choose a folder, not the whole drive %s", dir)
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("%s is a file, not a folder", dir)
	}
	return dir, nil
}

// Validation is the outcome of checking a proposed install folder.
type Validation struct {
	OK       bool   `json:"ok"`
	Resolved string `json:"resolved,omitempty"` // the folder that would be used
	Message  string `json:"message,omitempty"`  // why it is not acceptable
	Note     string `json:"note,omitempty"`     // something worth knowing about an acceptable folder
}

// ValidateDir checks dir the way Install will, without changing anything.
func ValidateDir(scope Scope, dir string) Validation {
	resolved, err := ResolveDir(scope, dir)
	if err != nil {
		return Validation{Message: capitalize(err.Error())}
	}
	v := Validation{OK: true, Resolved: resolved}
	if entries, err := os.ReadDir(resolved); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			v.Note = "This folder does not exist yet; it will be created."
		}
	} else if len(entries) > 0 {
		v.Note = "This folder already has files in it. Only " + exeName + " will be added."
	}
	return v
}

// Install installs S-Lab for scope into dir (empty means the scope's default
// folder), prompting for elevation if needed. progress may be nil.
func Install(scope Scope, dir string, progress Progress) Result {
	dir, err := ResolveDir(scope, dir)
	if err != nil {
		return Result{Message: capitalize(err.Error())}
	}
	return withElevation(scope, "install", []string{"-path", dir}, progress,
		func(s Scope, p Progress) Result { return doInstall(s, dir, p) })
}

// Uninstall removes the S-Lab installation for scope, prompting for elevation if needed.
func Uninstall(scope Scope, progress Progress) Result {
	return withElevation(scope, "uninstall", nil, progress, doUninstall)
}

// Doctor runs read-only diagnostics over every installation found.
func Doctor() Result { return doDoctor() }

// withElevation runs fn directly, or in an elevated worker process when the
// machine scope is requested without administrator rights. extra holds
// additional worker arguments to pass along. The worker reports its steps and
// result as JSON lines in a temporary file, which is followed here so progress
// still arrives live.
func withElevation(scope Scope, action string, extra []string, progress Progress, fn func(Scope, Progress) Result) Result {
	if !supported {
		return unsupported()
	}
	if scope != ScopeMachine || isElevated() {
		return fn(scope, progress)
	}

	f, err := os.CreateTemp("", "slab-result-*.jsonl")
	if err != nil {
		return failure("Could not create a temporary result file", err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	args := append([]string{"worker", action, "-scope", string(scope), "-result", path}, extra...)
	done := make(chan error, 1)
	go func() { done <- elevate(args) }()

	var result *Result
	offset := 0
	poll := func() {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for {
			i := bytes.IndexByte(b[offset:], '\n')
			if i < 0 {
				return // the rest is a line still being written
			}
			line := b[offset : offset+i]
			offset += i + 1
			var ev event
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			switch {
			case ev.Step != nil && progress != nil:
				progress(*ev.Step)
			case ev.Result != nil:
				result = ev.Result
			}
		}
	}

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var runErr error
wait:
	for {
		select {
		case runErr = <-done:
			break wait
		case <-tick.C:
			poll()
		}
	}
	poll()

	if runErr != nil {
		if errors.Is(runErr, errElevationDeclined) {
			return Result{Message: "Administrator permission was declined, so nothing was changed."}
		}
		return failure("The elevated helper failed", runErr)
	}
	if result == nil {
		return Result{Message: "The elevated helper produced no result."}
	}
	return *result
}

// RunWorker is the entry point for `worker <action> -scope <scope> [-path dir]`. With a
// result path it runs the action directly (it is the elevated child) and
// reports each step and the result there as JSON lines; without one it behaves
// like a normal CLI call and elevates itself if required (used by the Apps &
// features uninstall entry).
func RunWorker(action string, scope Scope, dir, resultPath string) error {
	if action != "install" && action != "uninstall" {
		return fmt.Errorf("unknown worker action %q", action)
	}

	if resultPath == "" {
		var r Result
		if action == "install" {
			r = Install(scope, dir, nil)
		} else {
			r = Uninstall(scope, nil)
		}
		fmt.Println(r.Message)
		for _, s := range r.Steps {
			fmt.Printf("  [%s] %s %s\n", s.Status, s.Name, s.Detail)
		}
		if !r.OK {
			return errors.New(r.Message)
		}
		return nil
	}

	f, err := os.OpenFile(resultPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	write := func(ev event) {
		b, _ := json.Marshal(ev)
		f.Write(append(b, '\n'))
	}
	progress := func(s Step) { write(event{Step: &s}) }

	var r Result
	if action == "install" {
		r = doInstall(scope, dir, progress)
	} else {
		r = doUninstall(scope, progress)
	}
	// The failure is reported through the result, not the exit code.
	write(event{Result: &r})
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func failure(msg string, err error) Result {
	if err != nil {
		msg += ": " + err.Error()
	}
	return Result{Message: msg}
}

func unsupported() Result {
	return Result{Message: "The S-Lab installer only supports Windows."}
}
