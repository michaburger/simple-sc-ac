// Package schemas holds the message definitions of the mioty interfaces, built into the
// programs. See README.md in this folder for the format.
package schemas

import "embed"

// FS contains the schema files.
//
//go:embed *.yaml
var FS embed.FS
