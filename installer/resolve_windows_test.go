//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(t.TempDir(), "My Apps", "S-Lab") // does not exist yet; spaces are fine

	tests := []struct {
		name, in string
		want     string // substring of the error; "" means success
	}{
		{"empty uses the default", "", ""},
		{"spaces only uses the default", "   ", ""},
		{"new folder with spaces", folder, ""},
		{"trailing slash is cleaned", folder + `\`, ""},
		{"relative path", `apps\slab`, "full path"},
		{"drive-relative path", `C:slab`, "full path"},
		{"drive root", `C:\`, "not the whole drive"},
		{"network share", `\\server\share\slab`, "network folders"},
		{"existing file", file, "is a file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDir(ScopeUser, tc.in)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("ResolveDir(%q) error = %v, want one containing %q", tc.in, err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveDir(%q): %v", tc.in, err)
			}
			if strings.TrimSpace(tc.in) == "" && got != defaultDir(ScopeUser) {
				t.Errorf("got %q, want the default %q", got, defaultDir(ScopeUser))
			}
			if got != filepath.Clean(got) {
				t.Errorf("result %q is not clean", got)
			}
		})
	}
}
