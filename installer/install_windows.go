//go:build windows

package installer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

const (
	supported = true
	dirName   = "S-Lab"
	// uninstallerName is the copy of this installer kept in the install folder
	// so the Apps & features entry keeps working wherever it was run from.
	uninstallerName = "uninstall.exe"
	appKey          = `Software\S-Lab`
	uninstallKey    = `Software\Microsoft\Windows\CurrentVersion\Uninstall\S-Lab`
	regAccess       = registry.WOW64_64KEY
)

func root(s Scope) registry.Key {
	if s == ScopeMachine {
		return registry.LOCAL_MACHINE
	}
	return registry.CURRENT_USER
}

func rootName(s Scope) string {
	if s == ScopeMachine {
		return "HKLM"
	}
	return "HKCU"
}

func defaultDir(s Scope) string {
	if s == ScopeMachine {
		return filepath.Join(os.Getenv("ProgramFiles"), dirName)
	}
	return filepath.Join(os.Getenv("LocalAppData"), "Programs", dirName)
}

// detect reads the installation record for scope, or nil if there is none.
func detect(s Scope) *Info {
	k, err := registry.OpenKey(root(s), appKey, registry.QUERY_VALUE|regAccess)
	if err != nil {
		return nil
	}
	defer k.Close()
	get := func(name string) string { v, _, _ := k.GetStringValue(name); return v }
	return &Info{
		Scope:       s,
		Version:     get("Version"),
		InstallPath: get("InstallPath"),
		InstallDate: get("InstallDate"),
	}
}

func doInstall(s Scope, dir string, progress Progress) Result {
	r := &reporter{progress: progress}
	fail := func(name string, err error) Result {
		r.end(name, "fail", err.Error())
		return Result{Message: "Installation failed: " + err.Error(), Steps: r.steps}
	}

	dir, err := ResolveDir(s, dir)
	if err != nil {
		return Result{Message: capitalize(err.Error())}
	}

	r.begin("Check install folder")
	prev := detect(s)
	detail := dir
	if prev != nil {
		detail += fmt.Sprintf(" (replaces version %s)", prev.Version)
	}
	r.end("Check install folder", "ok", detail)

	r.begin("Create install directory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail("Create install directory", err)
	}
	r.end("Create install directory", "ok", dir)

	exePath := filepath.Join(dir, exeName)
	r.begin("Copy " + exeName)
	if err := writeFileAtomic(exePath, payload); err != nil {
		return fail("Copy "+exeName, err)
	}
	r.end("Copy "+exeName, "ok", fmt.Sprintf("%d bytes", len(payload)))

	uninstallerPath := filepath.Join(dir, uninstallerName)
	r.begin("Copy uninstaller")
	if err := copySelf(uninstallerPath); err != nil {
		return fail("Copy uninstaller", err)
	}
	r.end("Copy uninstaller", "ok", uninstallerPath)

	r.begin("Write registry")
	if err := writeAppKey(s, dir, time.Now().Format(time.RFC3339)); err != nil {
		return fail("Write registry", err)
	}
	r.end("Write registry", "ok", rootName(s)+`\`+appKey)

	r.begin("Add to Apps & features")
	if err := writeUninstallEntry(s, dir, exePath, uninstallerPath); err != nil {
		return fail("Add to Apps & features", err)
	}
	r.end("Add to Apps & features", "ok", rootName(s)+`\`+uninstallKey)

	// Reinstalling into a different folder moves the app: drop the old copy.
	if prev != nil && prev.InstallPath != "" && !strings.EqualFold(filepath.Clean(prev.InstallPath), dir) {
		r.begin("Remove previous copy")
		r.end("Remove previous copy", "info", removeInstalled(prev.InstallPath))
	}

	return Result{
		OK:      true,
		Message: fmt.Sprintf("S-Lab %s installed for %s.", Version, scopeLabel(s)),
		Steps:   r.steps,
	}
}

func writeAppKey(s Scope, dir, date string) error {
	k, _, err := registry.CreateKey(root(s), appKey, registry.SET_VALUE|regAccess)
	if err != nil {
		return err
	}
	defer k.Close()
	for name, val := range map[string]string{
		"InstallPath": dir,
		"Version":     Version,
		"InstallDate": date,
		"Scope":       string(s),
	} {
		if err := k.SetStringValue(name, val); err != nil {
			return err
		}
	}
	return nil
}

// writeUninstallEntry adds the entry shown in Settings > Apps > Installed apps.
func writeUninstallEntry(s Scope, dir, exePath, uninstallerPath string) error {
	u, _, err := registry.CreateKey(root(s), uninstallKey, registry.SET_VALUE|regAccess)
	if err != nil {
		return err
	}
	defer u.Close()
	for name, val := range map[string]string{
		"DisplayName":     "S-Lab",
		"DisplayVersion":  Version,
		"Publisher":       "S-Lab",
		"InstallLocation": dir,
		"DisplayIcon":     exePath,
		"UninstallString": fmt.Sprintf(`"%s" worker uninstall -scope %s`, uninstallerPath, s),
	} {
		if err := u.SetStringValue(name, val); err != nil {
			return err
		}
	}
	for _, name := range []string{"NoModify", "NoRepair"} {
		if err := u.SetDWordValue(name, 1); err != nil {
			return err
		}
	}
	return u.SetDWordValue("EstimatedSize", uint32(len(payload)/1024+1))
}

func doUninstall(s Scope, progress Progress) Result {
	r := &reporter{progress: progress}

	info := detect(s)
	if info == nil {
		return Result{Message: "S-Lab is not installed for " + scopeLabel(s) + "."}
	}

	// The folder may be one the user chose, so remove only the file we
	// installed, and the folder itself only if that leaves it empty.
	if info.InstallPath != "" {
		exe := filepath.Join(info.InstallPath, exeName)
		r.begin("Remove files")
		if err := os.Remove(exe); err != nil && !errors.Is(err, os.ErrNotExist) {
			r.end("Remove files", "fail", err.Error())
			return Result{Message: "Uninstall failed: " + err.Error() + " (is S-Lab running?)", Steps: r.steps}
		}
		r.end("Remove files", "ok", exe)

		r.begin("Remove uninstaller")
		pending, err := removeUninstaller(info.InstallPath)
		if err != nil {
			r.end("Remove uninstaller", "fail", err.Error())
			return Result{Message: "Uninstall failed: " + err.Error(), Steps: r.steps}
		}
		if pending {
			r.end("Remove uninstaller", "info", "in use by this uninstall; it is removed as soon as this program exits")
		} else {
			r.end("Remove uninstaller", "ok", filepath.Join(info.InstallPath, uninstallerName))
		}

		r.begin("Remove folder")
		if pending {
			r.end("Remove folder", "info", info.InstallPath+" is removed once the uninstaller has exited")
		} else {
			r.end("Remove folder", "info", removeFolderIfEmpty(info.InstallPath))
		}
	}

	for _, k := range []struct{ name, key string }{
		{"Remove Apps & features entry", uninstallKey},
		{"Remove registry record", appKey},
	} {
		r.begin(k.name)
		if err := registry.DeleteKey(root(s), k.key); err != nil && !errors.Is(err, registry.ErrNotExist) {
			r.end(k.name, "fail", err.Error())
			return Result{Message: "Uninstall failed: " + err.Error(), Steps: r.steps}
		}
		r.end(k.name, "ok", rootName(s)+`\`+k.key)
	}
	return Result{OK: true, Message: "S-Lab was uninstalled for " + scopeLabel(s) + ".", Steps: r.steps}
}

// removeInstalled deletes the S-Lab executable and uninstaller from dir, and
// the folder if that leaves it empty.
func removeInstalled(dir string) string {
	if err := os.Remove(filepath.Join(dir, exeName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("could not remove %s from %s: %v", exeName, dir, err)
	}
	if pending, err := removeUninstaller(dir); err != nil {
		return fmt.Sprintf("could not remove %s from %s: %v", uninstallerName, dir, err)
	} else if pending {
		return dir + " left in place (it holds the program that is running)"
	}
	return removeFolderIfEmpty(dir)
}

// removeFolderIfEmpty removes dir only if nothing else is in it, and says what happened.
func removeFolderIfEmpty(dir string) string {
	if err := os.Remove(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dir + " was already gone"
		}
		return dir + " left in place (not empty)"
	}
	return dir + " removed"
}

func doDoctor() Result {
	var steps []Step
	add := func(group, name, status, detail string) {
		steps = append(steps, Step{Name: name, Status: status, Detail: detail, Group: group})
	}

	add("General", "Installer payload", "ok", fmt.Sprintf("S-Lab %s, %d bytes embedded", Version, len(payload)))
	if isElevated() {
		add("General", "Privileges", "info", "running as administrator")
	} else {
		add("General", "Privileges", "info", "standard user (all-users changes will ask for permission)")
	}

	found := 0
	for _, s := range []Scope{ScopeUser, ScopeMachine} {
		info := detect(s)
		if info == nil {
			add(groupName(s), "Installation", "info", "not installed")
			continue
		}
		found++
		steps = append(steps, checkInstall(s, info)...)
	}
	if found == 2 {
		add("General", "Multiple installs", "warn", "installed for both the current user and all users; the per-user copy may shadow the shared one")
	}

	ok, warns := true, 0
	for _, st := range steps {
		switch st.Status {
		case "fail":
			ok = false
		case "warn":
			warns++
		}
	}
	msg := "No S-Lab installation found."
	switch {
	case found > 0 && !ok:
		msg = "Problems were found with the S-Lab installation."
	case found > 0 && warns > 0:
		msg = "S-Lab is installed, with warnings."
	case found > 0:
		msg = "S-Lab is installed and healthy."
	}
	return Result{OK: ok, Message: msg, Steps: steps}
}

func checkInstall(s Scope, info *Info) []Step {
	var steps []Step
	add := func(name, status, detail string) {
		steps = append(steps, Step{Name: name, Status: status, Detail: detail, Group: groupName(s)})
	}

	if info.InstallPath == "" || info.Version == "" {
		add("Registry record", "fail", "InstallPath or Version is missing")
		return steps
	}
	add("Registry record", "ok", fmt.Sprintf("version %s, installed %s", info.Version, info.InstallDate))

	if info.Version != Version {
		add("Version", "warn", fmt.Sprintf("installed %s, this installer carries %s", info.Version, Version))
	} else {
		add("Version", "ok", info.Version)
	}

	if fi, err := os.Stat(info.InstallPath); err != nil || !fi.IsDir() {
		add("Install directory", "fail", info.InstallPath+" is missing")
	} else {
		add("Install directory", "ok", info.InstallPath)
	}

	exePath := filepath.Join(info.InstallPath, exeName)
	got, err := os.ReadFile(exePath)
	switch {
	case err != nil:
		add("Executable", "fail", exeName+" is missing")
	case info.Version == Version && sha256.Sum256(got) != sha256.Sum256(payload):
		add("Executable", "fail", exeName+" does not match the installer's copy (modified or corrupt)")
	case info.Version == Version && bytes.Equal(got, payload):
		add("Executable", "ok", exeName+" matches the installer's copy")
	default:
		add("Executable", "ok", exeName+" is present")
	}

	if _, err := os.Stat(filepath.Join(info.InstallPath, uninstallerName)); err != nil {
		add("Uninstaller", "warn", uninstallerName+" is missing; reinstall to restore it")
	} else {
		add("Uninstaller", "ok", uninstallerName+" is present")
	}

	u, err := registry.OpenKey(root(s), uninstallKey, registry.QUERY_VALUE|regAccess)
	if err != nil {
		add("Apps & features entry", "fail", "uninstall entry is missing")
		return steps
	}
	defer u.Close()
	if loc, _, _ := u.GetStringValue("InstallLocation"); loc != info.InstallPath {
		add("Apps & features entry", "warn", fmt.Sprintf("points to %q instead of %q", loc, info.InstallPath))
	} else {
		add("Apps & features entry", "ok", "present and consistent")
	}
	if target := uninstallTarget(regString(u, "UninstallString")); target == "" {
		add("Uninstall command", "warn", "the Apps & features entry has no uninstall command")
	} else if _, err := os.Stat(target); err != nil {
		add("Uninstall command", "warn", target+" no longer exists, so uninstalling from Settings will fail; reinstall to fix it")
	} else {
		add("Uninstall command", "ok", "runs "+target)
	}
	return steps
}

// groupName is the heading Doctor lists an installation's checks under.
func groupName(s Scope) string {
	if s == ScopeMachine {
		return "All users"
	}
	return "Current user"
}

func regString(k registry.Key, name string) string {
	v, _, _ := k.GetStringValue(name)
	return v
}

func scopeLabel(s Scope) string {
	if s == ScopeMachine {
		return "all users"
	}
	return "current user"
}

// writeFileAtomic writes via a temp file in the same directory, then renames.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
