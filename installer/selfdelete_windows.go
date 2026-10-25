//go:build windows

package installer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// scheduleSelfCleanup removes the installed uninstaller, and then the install
// folder, after this process has exited, if this process is that uninstaller.
// A running program cannot delete its own file on Windows, so a detached,
// hidden cmd.exe keeps trying until the file is free.
func scheduleSelfCleanup(dir string) {
	self, err := os.Executable()
	if err != nil || !within(dir, self) {
		return
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       "cmd.exe " + cleanupArgs(self, dir),
		CreationFlags: 0x08000000 | 0x00000200, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP
	}
	cmd.Start() // fire and forget: it must outlive this process
}

// cleanupArgs is the cmd.exe argument string that deletes exe (retrying for up
// to about 30 seconds while it is still running), then removes dir if empty.
func cleanupArgs(exe, dir string) string {
	return fmt.Sprintf(`/D /S /C "for /L %%i in (1,1,15) do @(if exist "%[1]s" (del /F /Q "%[1]s" 2>nul & ping -n 3 127.0.0.1 >nul)) & rmdir "%[2]s" 2>nul"`, exe, dir)
}

// within reports whether path is inside dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// uninstallTarget extracts the program from an UninstallString such as
// `"C:\Program Files\S-Lab\uninstall.exe" worker uninstall -scope machine`.
func uninstallTarget(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.Index(cmd[1:], `"`); end >= 0 {
			return cmd[1 : 1+end]
		}
		return ""
	}
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

func sameFile(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

// copySelf copies the running executable to dest, so the install folder holds
// an uninstaller that no longer depends on where the installer was run from.
// It does nothing if this program already is dest (reinstalling from there).
func copySelf(dest string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if sameFile(self, dest) {
		return nil
	}
	b, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	return writeFileAtomic(dest, b)
}

// removeUninstaller deletes the uninstaller copy in dir. If this process is
// that file it cannot be deleted yet; pending is then true and
// scheduleSelfCleanup finishes the job once the process has exited.
func removeUninstaller(dir string) (pending bool, err error) {
	p := filepath.Join(dir, uninstallerName)
	if self, e := os.Executable(); e == nil && sameFile(self, p) {
		return true, nil
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return false, nil
}
