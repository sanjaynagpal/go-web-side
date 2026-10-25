# go-web-side

Go CLI with web UI.

The example application is a mock installer for a desktop app called **S-Lab**.
It can **install**, **uninstall** and run **doctor** (diagnostics), either from
a browser window that the CLI opens for you or directly in the terminal.

The installer is Windows-only: it copies the embedded `slab.exe` to disk and
records the installation in the registry. On other platforms it builds and
runs, but every action reports that it is unsupported.

## Run it

```
go generate ./...   # builds installer/payload/slab.exe (not committed)
go run .
```

`go run .` starts a local server on a free port and opens the UI in a
chromeless Chrome or Edge window (whichever is your default browser, falling
back to the other, then to your default browser's normal tab). Close the window
and the server exits. Press Ctrl+C to stop it sooner.

| Flag          | Effect                                                    |
| ------------- | --------------------------------------------------------- |
| `-port N`     | Listen on port `N` instead of a free port                 |
| `-no-browser` | Don't open a window; just print the URL                   |
| `-keep-alive` | Keep running after the window is closed                   |

## Terminal version

```
go run . cli                         # interactive menu
go run . cli install                 # for the current user (default)
go run . cli install -scope machine  # for all users (asks for administrator permission)
go run . cli install -path "D:\My Apps\S-Lab"   # custom install folder
go run . cli uninstall [-scope user|machine]
go run . cli doctor
```

Direct commands exit with status 1 if the action did not succeed. Set
`NO_COLOR=1` for plain output; output is also plain when piped.

## What it does

- **Install** for the current user (`%LocalAppData%\Programs\S-Lab`, `HKCU`) or
  for all users (`%ProgramFiles%\S-Lab`, `HKLM`). Installing for all users
  needs elevation: the server stays unelevated and relaunches the CLI as a
  hidden `worker` through a UAC prompt. If you decline, nothing changes.
  The install folder can be changed (the "Install folder" field in the UI, with
  a **Browse…** button that opens the Windows folder dialog, or `cli install
  -path`). If the folder you pick already has other files in it, S-Lab goes in
  an `S-Lab` subfolder. The folder must be a full local path, not a drive root or a
  network share; this is checked before any UAC prompt. Installing again into a
  different folder moves the app and removes the old copy.
- **Uninstall** reads the registry record (which includes the chosen folder) and
  removes the registry keys and the installed `slab.exe`. Because the folder may
  be one you picked, it never deletes the folder wholesale: it removes the
  folder only if it is empty afterwards and otherwise leaves your other files.
- **Live progress.** In the web UI each install/uninstall step appears in a
  checklist as it happens (the server streams newline-delimited JSON; for an
  all-users install the elevated helper reports through a temp file the server
  follows). Steps are paced by a short delay (`installer.StepDelay`) so the
  progress is visible; the terminal CLI turns that delay off.
- **Folder validation.** The Install tab checks the folder as you type (the same
  rules the installer uses) and disables Install until it is usable.
- **Doctor** is read-only. It checks the registry record, version, install
  folder, that `slab.exe` matches the copy embedded in the installer, that the
  uninstaller exists, and that the Settings > Apps entry and its uninstall
  command point at files that still exist, and warns if both a per-user and an all-users install
  exist.

An "Installed apps" entry is written so S-Lab shows up in Windows Settings. The
installer copies itself into the install folder as `uninstall.exe` and the
entry's uninstall command runs that copy, so it keeps working wherever the
installer was run from, including `go run`. A running program cannot delete its
own file, so when the uninstaller removes itself it starts a short hidden
helper that deletes it (and the folder, if empty) as soon as it has exited.

## Layout

| Path                    | Purpose                                                        |
| ----------------------- | -------------------------------------------------------------- |
| `main.go`               | Entry point: web mode, `cli`, and the hidden `worker` mode     |
| `browser.go`            | Opens the UI in a Chrome/Edge app window                       |
| `cli.go`, `term*.go`    | Terminal menu, colors and boxes                                |
| `web/`                  | HTTP handlers, session tracking, embedded UI (`web/static`)    |
| `installer/`            | Install, uninstall and doctor logic; embeds the S-Lab payload  |
| `cmd/slab/`             | Placeholder S-Lab application that gets embedded               |

## Security notes

The server listens on `127.0.0.1` only. It rejects requests whose `Host` or
`Origin` doesn't match its own address, and POSTs that aren't `application/json`,
so a web page elsewhere can't trigger an install through localhost.

## Development

```
go vet ./...
go test ./...
```

Re-run `go generate ./...` after changing `cmd/slab`.
