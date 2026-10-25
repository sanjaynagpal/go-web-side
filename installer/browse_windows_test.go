//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuggestDir(t *testing.T) {
	base := t.TempDir()
	empty := filepath.Join(base, "empty")
	busy := filepath.Join(base, "busy")
	named := filepath.Join(base, "s-lab") // already named like the app, different case
	for _, d := range []string{empty, busy, named} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(busy, "x.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(named, "x.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct{ name, in, want string }{
		{"empty folder is used as is", empty, empty},
		{"non-empty folder gets a subfolder", busy, filepath.Join(busy, "S-Lab")},
		{"folder already named S-Lab is used as is", named, named},
		{"drive root gets a subfolder", `D:\`, `D:\S-Lab`},
	}
	for _, tc := range tests {
		if got := suggestDir(tc.in); got != tc.want {
			t.Errorf("%s: suggestDir(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestExistingParent(t *testing.T) {
	base := t.TempDir()
	if got := existingParent(filepath.Join(base, "no", "such", "dir")); got != base {
		t.Errorf("got %q, want nearest existing parent %q", got, base)
	}
	for _, in := range []string{"", "   ", `relative\path`} {
		if got := existingParent(in); got != "" {
			t.Errorf("existingParent(%q) = %q, want empty", in, got)
		}
	}
}

// The picker script must build its form and dialog objects without errors.
// ShowDialog is swapped for a stub so no window appears.
func TestPickScriptRuns(t *testing.T) {
	const show = "$dlg.ShowDialog($owner)"
	if !strings.Contains(pickScript, show) {
		t.Fatal("pickScript no longer contains the ShowDialog call this test stubs out")
	}
	script := strings.Replace(pickScript, show, "[System.Windows.Forms.DialogResult]::Cancel", 1)
	out, err := runPicker(script, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("a cancelled dialog should print nothing, got %q", out)
	}
}
