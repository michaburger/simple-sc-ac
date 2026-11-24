// Package buildinfo holds the version string of the programs. It is set at build time:
//
//	go build -ldflags "-X github.com/michaburger/simple-sc-ac/internal/common/buildinfo.Version=0.1.0" ./cmd/...
package buildinfo

// Version is the release version, "dev" for local builds.
var Version = "dev"
