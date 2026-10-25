package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// open launches url in a chromeless Chrome or Edge app window, preferring whichever is
// the default browser. It falls back to the system URL handler (usually a tab
// in the default browser) if neither is found.
func open(url string) error {
	for _, exe := range candidates() {
		if exe != "" {
			return exec.Command(exe, "--app="+url, "--window-size=700,640").Start()
		}
	}
	return openDefault(url)
}

func openDefault(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// candidates returns installed Chrome/Edge executables, default browser first.
func candidates() []string {
	chrome, edge := findChrome(), findEdge()
	if defaultBrowser() == "edge" {
		return []string{edge, chrome}
	}
	return []string{chrome, edge}
}

// defaultBrowser reports "chrome", "edge" or "" for the user's default https handler.
func defaultBrowser() string {
	var out []byte
	var err error
	switch runtime.GOOS {
	case "windows":
		out, err = exec.Command("reg", "query",
			`HKCU\Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`,
			"/v", "ProgId").Output()
	case "linux":
		out, err = exec.Command("xdg-settings", "get", "default-web-browser").Output()
	default:
		return ""
	}
	if err != nil {
		return ""
	}
	s := strings.ToLower(string(out))
	switch {
	case strings.Contains(s, "edge"):
		return "edge"
	case strings.Contains(s, "chrome"):
		return "chrome"
	}
	return ""
}

func findChrome() string {
	return find([]string{"google-chrome", "google-chrome-stable", "chrome", "chromium"},
		`Google\Chrome\Application\chrome.exe`,
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
}

func findEdge() string {
	return find([]string{"microsoft-edge", "microsoft-edge-stable", "msedge"},
		`Microsoft\Edge\Application\msedge.exe`,
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge")
}

func find(names []string, winRel, macPath string) string {
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if base := os.Getenv(env); base != "" {
				p := filepath.Join(base, winRel)
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	case "darwin":
		if _, err := os.Stat(macPath); err == nil {
			return macPath
		}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}
