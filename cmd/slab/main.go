// Command slab is a stand-in for the S-Lab desktop application. The installer
// embeds its built executable (see installer/installer.go for go:generate).
package main

import "fmt"

const version = "1.0.0"

func main() {
	fmt.Println("S-Lab", version)
}
