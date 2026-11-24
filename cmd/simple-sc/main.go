// Command simple-sc is the mioty Service Center of simple-sc-ac.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/michaburger/simple-sc-ac/internal/common/buildinfo"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("simple-sc", buildinfo.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "simple-sc", buildinfo.Version, "- not functional yet, see README.md")
	os.Exit(1)
}
