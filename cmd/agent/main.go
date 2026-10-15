// Command agent runs on every Node. It applies what the Coordinator decides,
// measures Links, and reports health and usage.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	fmt.Fprintln(os.Stderr, "agent: not implemented yet")
	os.Exit(1)
}
