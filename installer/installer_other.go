//go:build !windows

package installer

const supported = false

func detect(Scope) *Info             { return nil }
func isElevated() bool               { return false }
func runElevated([]string) error     { return errElevationDeclined }
func defaultDir(Scope) string        { return "" }
func doInstall(Scope, string) Result { return unsupported() }
func doUninstall(Scope) Result       { return unsupported() }
func doDoctor() Result               { return unsupported() }
