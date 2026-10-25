# Builds go-web-side.exe, a single self-contained Windows program that others can
# download and double-click, and a zip of it that is easy to share.
#
#   .\build.ps1
#
# Output goes to dist\:
#   go-web-side.exe                 the program
#   go-web-side-windows-amd64.zip   the program and the README
#   SHA256SUMS.txt                  checksums, so a download can be verified
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

# Builds the embedded S-Lab app and the executable's icon and version info.
go generate ./...
if ($LASTEXITCODE -ne 0) { throw "go generate failed" }

New-Item -ItemType Directory -Force dist | Out-Null
Remove-Item dist\* -Force -Recurse

# -trimpath and -s -w drop local paths and debug symbols: smaller, and the same
# result on any machine.
go build -trimpath -ldflags "-s -w" -o dist\go-web-side.exe .
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

Compress-Archive -Path dist\go-web-side.exe, README.md -DestinationPath dist\go-web-side-windows-amd64.zip

Get-ChildItem dist\go-web-side.exe, dist\go-web-side-windows-amd64.zip | ForEach-Object {
    "{0}  {1}" -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower(), $_.Name
} | Set-Content dist\SHA256SUMS.txt -Encoding ascii

Get-ChildItem dist | Format-Table Name, @{n = "MB"; e = { [math]::Round($_.Length / 1MB, 2) }} -AutoSize
