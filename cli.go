package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"go-web-side/installer"
)

const cliUsage = `usage:
  go-web-side cli                                   interactive menu
  go-web-side cli install   [-scope user|machine] [-path folder]   (default scope: user)
  go-web-side cli uninstall [-scope user|machine]   (default: whichever is installed)
  go-web-side cli doctor`

// cli is the terminal counterpart of the web UI. With no arguments it shows
// an interactive menu; with an action it runs that action and exits non-zero
// if it did not succeed.
func cli(args []string) error {
	if len(args) == 0 {
		return menu(bufio.NewReader(os.Stdin))
	}

	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	scopeFlag := fs.String("scope", "", "user or machine")
	pathFlag := fs.String("path", "", "install folder (install only; default: the scope's standard location)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	var scope installer.Scope
	if *scopeFlag != "" {
		var err error
		if scope, err = installer.ParseScope(*scopeFlag); err != nil {
			return err
		}
	}

	var r installer.Result
	switch args[0] {
	case "install":
		if scope == "" {
			scope = installer.ScopeUser
		}
		r = installer.Install(scope, *pathFlag)
	case "uninstall":
		if scope == "" {
			st := installer.GetStatus()
			switch {
			case st.User != nil && st.Machine != nil:
				return errors.New("S-Lab is installed for both; pass -scope user or -scope machine")
			case st.Machine != nil:
				scope = installer.ScopeMachine
			default:
				scope = installer.ScopeUser
			}
		}
		r = installer.Uninstall(scope)
	case "doctor":
		r = installer.Doctor()
	default:
		return errors.New(cliUsage)
	}
	printResult(r)
	if !r.OK {
		os.Exit(1)
	}
	return nil
}

func menu(in *bufio.Reader) error {
	for {
		st := installer.GetStatus()
		fmt.Println()
		box("S-Lab Setup "+st.Version, cyan, []string{
			dim("Current user: ") + describe(st.User),
			dim("All users:    ") + describe(st.Machine),
		})
		fmt.Println()
		option("1", "Install")
		option("2", "Uninstall")
		option("3", "Doctor (diagnostics)")
		option("4", "Quit")

		choice, err := ask(in, "Choose [1-4]: ")
		if err != nil {
			return quitOnEOF(err)
		}
		switch choice {
		case "1":
			err = menuInstall(in)
		case "2":
			err = menuUninstall(in, st)
		case "3":
			printResult(installer.Doctor())
		case "4", "q", "quit":
			return nil
		default:
			fmt.Println("Please enter 1, 2, 3 or 4.")
		}
		if err != nil {
			return quitOnEOF(err)
		}
	}
}

func menuInstall(in *bufio.Reader) error {
	fmt.Println("\n" + bold("Install for:"))
	option("1", "Just me")
	option("2", "All users on this computer "+yellow("(asks for administrator permission)"))
	option("3", "Back")
	choice, err := ask(in, "Choose [1-3]: ")
	if err != nil {
		return err
	}
	var scope installer.Scope
	switch choice {
	case "1":
		scope = installer.ScopeUser
	case "2":
		scope = installer.ScopeMachine
	default:
		return nil
	}

	def := installer.GetStatus().DefaultDirs[scope]
	dir, err := ask(in, fmt.Sprintf("Install folder %s: ", dim("["+def+"]")))
	if err != nil {
		return err
	}
	if scope == installer.ScopeMachine {
		fmt.Println("Waiting for administrator permission...")
	}
	printResult(installer.Install(scope, dir))
	return nil
}

func menuUninstall(in *bufio.Reader, st installer.Status) error {
	var found []*installer.Info
	for _, i := range []*installer.Info{st.User, st.Machine} {
		if i != nil {
			found = append(found, i)
		}
	}
	if len(found) == 0 {
		fmt.Println("\nS-Lab is not installed.")
		return nil
	}

	target := found[0]
	if len(found) > 1 {
		fmt.Println("\n" + bold("Uninstall from:"))
		option("1", "Current user")
		option("2", "All users "+yellow("(asks for administrator permission)"))
		option("3", "Back")
		choice, err := ask(in, "Choose [1-3]: ")
		if err != nil {
			return err
		}
		switch choice {
		case "1":
			target = st.User
		case "2":
			target = st.Machine
		default:
			return nil
		}
	}

	ok, err := ask(in, fmt.Sprintf("Uninstall S-Lab from %s? [y/N]: ", target.InstallPath))
	if err != nil {
		return err
	}
	if strings.EqualFold(ok, "y") || strings.EqualFold(ok, "yes") {
		printResult(installer.Uninstall(target.Scope))
	}
	return nil
}

func describe(i *installer.Info) string {
	if i == nil {
		return "not installed"
	}
	return fmt.Sprintf("v%s at %s", i.Version, i.InstallPath)
}

// option prints one numbered menu choice.
func option(key, text string) {
	fmt.Printf("  %s  %s\n", bold(cyan(key)), text)
}

// printResult shows an action's outcome in a green or red box, with each
// step coloured by status, so it is clearly output and not a choice.
func printResult(r installer.Result) {
	color := red
	if r.OK {
		color = green
	}
	var lines []string
	for _, s := range r.Steps {
		tag := fmt.Sprintf("%-4s", s.Status)
		switch s.Status {
		case "ok":
			tag = green(tag)
		case "warn":
			tag = yellow(tag)
		case "fail":
			tag = red(tag)
		default:
			tag = dim(tag)
		}
		line := tag + "  " + s.Name
		if s.Detail != "" {
			line += dim(" - " + s.Detail)
		}
		lines = append(lines, line)
	}
	lines = append(lines, bold(color(r.Message)))
	fmt.Println()
	box("Result", color, lines)
}

func ask(in *bufio.Reader, prompt string) (string, error) {
	fmt.Print(bold(prompt))
	line, err := in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// quitOnEOF treats end of input (Ctrl+Z / Ctrl+D, closed pipe) as a normal exit.
func quitOnEOF(err error) error {
	if errors.Is(err, io.EOF) {
		fmt.Println()
		return nil
	}
	return err
}
