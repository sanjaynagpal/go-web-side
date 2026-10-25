//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallTarget(t *testing.T) {
	tests := []struct{ in, want string }{
		{`"C:\Program Files\S-Lab\uninstall.exe" worker uninstall -scope machine`, `C:\Program Files\S-Lab\uninstall.exe`},
		{`C:\Apps\uninstall.exe worker uninstall`, `C:\Apps\uninstall.exe`},
		{`  "C:\a b\x.exe"  `, `C:\a b\x.exe`},
		{`C:\Apps\x.exe`, `C:\Apps\x.exe`},
		{`"unterminated`, ``},
		{``, ``},
	}
	for _, tc := range tests {
		if got := uninstallTarget(tc.in); got != tc.want {
			t.Errorf("uninstallTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		dir, path string
		want      bool
	}{
		{`C:\Apps\S-Lab`, `C:\Apps\S-Lab\uninstall.exe`, true},
		{`C:\Apps\S-Lab`, `c:\apps\s-lab\sub\x.exe`, true}, // Windows paths ignore case
		{`C:\Apps\S-Lab`, `C:\Apps\S-Lab`, false},
		{`C:\Apps\S-Lab`, `C:\Apps\S-Lab2\x.exe`, false}, // a sibling with the same prefix
		{`C:\Apps\S-Lab`, `C:\Other\x.exe`, false},
		{`C:\Apps\S-Lab`, `D:\Apps\S-Lab\x.exe`, false},
	}
	for _, tc := range tests {
		if got := within(tc.dir, tc.path); got != tc.want {
			t.Errorf("within(%q, %q) = %v, want %v", tc.dir, tc.path, got, tc.want)
		}
	}
}

func TestCleanupArgsQuotesBothPaths(t *testing.T) {
	exe := `C:\Program Files\My Apps\S-Lab\uninstall.exe`
	dir := `C:\Program Files\My Apps\S-Lab`
	args := cleanupArgs(exe, dir)
	for _, want := range []string{`/D /S /C "`, `if exist "` + exe + `"`, `del /F /Q "` + exe + `"`, `rmdir "` + dir + `"`} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q:\n%s", want, args)
		}
	}
	if !strings.HasSuffix(args, `"`) || strings.Count(args, `%i`) < 1 || strings.Contains(args, "%%") {
		t.Errorf("args malformed (FOR variable must be a single %%i):\n%s", args)
	}
}

func TestCopySelfSkipsWhenAlreadyThere(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copySelf(self); err != nil { // would fail or corrupt if it rewrote the running exe
		t.Fatalf("copySelf onto itself: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "nested", "uninstall.exe")
	os.MkdirAll(filepath.Dir(dest), 0o755)
	if err := copySelf(dest); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(self)
	b, err := os.Stat(dest)
	if err != nil || a.Size() != b.Size() {
		t.Errorf("copy missing or wrong size: %v (src %d bytes)", err, a.Size())
	}
}
