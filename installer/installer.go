// Package installer is a mock installer for the S-Lab desktop application. It
// copies the embedded executable to disk, records the installation in the
// Windows registry, and can later uninstall or diagnose it from that record.
package installer

//go:generate go build -o payload/slab.exe ../cmd/slab

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

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
	Status string `json:"status"` // ok, warn, fail or info
	Detail string `json:"detail,omitempty"`
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
}

var errElevationDeclined = errors.New("elevation was declined")

// GetStatus reports which installations exist.
func GetStatus() Status {
	return Status{
		Supported: supported,
		Elevated:  isElevated(),
		Version:   Version,
		User:      detect(ScopeUser),
		Machine:   detect(ScopeMachine),
	}
}

// Install installs S-Lab for scope, prompting for elevation if needed.
func Install(scope Scope) Result {
	return withElevation(scope, "install", doInstall)
}

// Uninstall removes the S-Lab installation for scope, prompting for elevation if needed.
func Uninstall(scope Scope) Result {
	return withElevation(scope, "uninstall", doUninstall)
}

// Doctor runs read-only diagnostics over every installation found.
func Doctor() Result { return doDoctor() }

// withElevation runs fn directly, or in an elevated worker process when the
// machine scope is requested without administrator rights.
func withElevation(scope Scope, action string, fn func(Scope) Result) Result {
	if !supported {
		return unsupported()
	}
	if scope != ScopeMachine || isElevated() {
		return fn(scope)
	}

	f, err := os.CreateTemp("", "slab-result-*.json")
	if err != nil {
		return failure("Could not create a temporary result file", err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	if err := runElevated([]string{"worker", action, "-scope", string(scope), "-result", path}); err != nil {
		if errors.Is(err, errElevationDeclined) {
			return Result{Message: "Administrator permission was declined, so nothing was changed."}
		}
		return failure("The elevated helper failed", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return failure("The elevated helper produced no result", err)
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return failure("The elevated helper returned an unreadable result", err)
	}
	return r
}

// RunWorker is the entry point for `worker <action> -scope <scope>`. With a
// result path it runs the action directly (it is the elevated child) and
// writes JSON there; without one it behaves like a normal CLI call and
// elevates itself if required (used by the Apps & features uninstall entry).
func RunWorker(action string, scope Scope, resultPath string) error {
	var r Result
	switch {
	case action == "install" && resultPath != "":
		r = doInstall(scope)
	case action == "uninstall" && resultPath != "":
		r = doUninstall(scope)
	case action == "install":
		r = Install(scope)
	case action == "uninstall":
		r = Uninstall(scope)
	default:
		return fmt.Errorf("unknown worker action %q", action)
	}

	if resultPath != "" {
		b, _ := json.Marshal(r)
		// The failure is reported through the result file, not the exit code.
		return os.WriteFile(resultPath, b, 0o600)
	} else {
		fmt.Println(r.Message)
		for _, s := range r.Steps {
			fmt.Printf("  [%s] %s %s\n", s.Status, s.Name, s.Detail)
		}
	}
	if !r.OK {
		return errors.New(r.Message)
	}
	return nil
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
