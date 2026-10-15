// Command coordinator runs the DARZ Coordinator: identity, topology, policy and metering for the mesh.
// It never touches a packet on the data path.
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
	fmt.Fprintln(os.Stderr, "coordinator: not implemented yet")
	os.Exit(1)
}
