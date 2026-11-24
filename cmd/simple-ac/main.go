// Command simple-ac is the mioty Application Center of simple-sc-ac.
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
		fmt.Println("simple-ac", buildinfo.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "simple-ac", buildinfo.Version, "- not functional yet, see README.md")
	os.Exit(1)
}
