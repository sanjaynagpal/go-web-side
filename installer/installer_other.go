//go:build !windows

package installer

const supported = false

func detect(Scope) *Info         { return nil }
func isElevated() bool           { return false }
func runElevated([]string) error { return errElevationDeclined }
func doInstall(Scope) Result     { return unsupported() }
func doUninstall(Scope) Result   { return unsupported() }
func doDoctor() Result           { return unsupported() }
