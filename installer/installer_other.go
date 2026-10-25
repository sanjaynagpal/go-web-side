//go:build !windows

package installer

import "errors"

const supported = false

func detect(Scope) *Info                       { return nil }
func isElevated() bool                         { return false }
func runElevated([]string) error               { return errElevationDeclined }
func defaultDir(Scope) string                  { return "" }
func doInstall(Scope, string, Progress) Result { return unsupported() }
func doUninstall(Scope, Progress) Result       { return unsupported() }
func doDoctor() Result                         { return unsupported() }

// PickFolder is only implemented on Windows.
func PickFolder(string) (string, bool, error) {
	return "", false, errors.New("the folder picker is only available on Windows")
}
