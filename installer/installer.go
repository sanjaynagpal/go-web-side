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
	"path/filepath"
	"strings"
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

	// DefaultDirs is where each scope installs unless told otherwise.
	DefaultDirs map[Scope]string `json:"defaultDirs"`
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

// Install installs S-Lab for scope into dir (empty means the scope's default
// folder), prompting for elevation if needed.
func Install(scope Scope, dir string) Result {
	dir, err := ResolveDir(scope, dir)
	if err != nil {
		return Result{Message: capitalize(err.Error())}
	}
	return withElevation(scope, "install", []string{"-path", dir}, func(s Scope) Result { return doInstall(s, dir) })
}

// Uninstall removes the S-Lab installation for scope, prompting for elevation if needed.
func Uninstall(scope Scope) Result {
	return withElevation(scope, "uninstall", nil, doUninstall)
}

// Doctor runs read-only diagnostics over every installation found.
func Doctor() Result { return doDoctor() }

// withElevation runs fn directly, or in an elevated worker process when the
// machine scope is requested without administrator rights. extra holds
// additional worker arguments to pass along.
func withElevation(scope Scope, action string, extra []string, fn func(Scope) Result) Result {
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

	args := append([]string{"worker", action, "-scope", string(scope), "-result", path}, extra...)
	if err := runElevated(args); err != nil {
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

// RunWorker is the entry point for `worker <action> -scope <scope> [-path dir]`. With a
// result path it runs the action directly (it is the elevated child) and
// writes JSON there; without one it behaves like a normal CLI call and
// elevates itself if required (used by the Apps & features uninstall entry).
func RunWorker(action string, scope Scope, dir, resultPath string) error {
	var r Result
	switch {
	case action == "install" && resultPath != "":
		r = doInstall(scope, dir)
	case action == "uninstall" && resultPath != "":
		r = doUninstall(scope)
	case action == "install":
		r = Install(scope, dir)
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
