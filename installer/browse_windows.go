//go:build windows

package installer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// pickScript shows the standard Windows folder dialog and prints the chosen
// folder to stdout (nothing if cancelled). The starting folder comes from the
// environment so it is never interpreted as script text.
const pickScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
$dlg.Description = 'Choose the folder to install S-Lab into'
$dlg.ShowNewFolderButton = $true
if ($env:SLAB_INITIAL_DIR) { $dlg.SelectedPath = $env:SLAB_INITIAL_DIR }
if ($dlg.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($dlg.SelectedPath) }
`

// PickFolder opens a native folder dialog starting at initial (or its nearest
// existing parent) and returns the install folder to use. ok is false if the
// user cancelled. It blocks until the dialog is closed.
func PickFolder(initial string) (dir string, ok bool, err error) {
	out, err := runPicker(pickScript, initial)
	if err != nil {
		return "", false, err
	}
	picked := strings.TrimSpace(out)
	if picked == "" {
		return "", false, nil
	}
	return suggestDir(picked), true, nil
}

func runPicker(script, initial string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "SLAB_INITIAL_DIR="+existingParent(initial))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("the folder dialog failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("could not open the folder dialog: %w", err)
	}
	return string(out), nil
}

// existingParent returns dir, or its nearest parent that exists, or "" if dir
// is not a usable absolute path. The dialog can only start in a real folder.
func existingParent(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	for dir = filepath.Clean(dir); ; dir = filepath.Dir(dir) {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// suggestDir turns the folder the user picked into the folder to install into.
// A folder already called S-Lab, or an empty one, is used as is; otherwise the
// app goes in an S-Lab subfolder so it does not land among unrelated files.
func suggestDir(picked string) string {
	picked = filepath.Clean(picked)
	if filepath.Dir(picked) == picked { // a drive root cannot be an install folder
		return filepath.Join(picked, dirName)
	}
	if strings.EqualFold(filepath.Base(picked), dirName) {
		return picked
	}
	if entries, err := os.ReadDir(picked); err == nil && len(entries) == 0 {
		return picked
	}
	return filepath.Join(picked, dirName)
}
