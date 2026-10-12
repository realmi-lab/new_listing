// Package webui embeds the built screen so the program is one file.
// scripts/build.sh copies web/dist here before compiling.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

// FS returns the built screen, and false when it was not built into this binary.
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return sub, false
	}
	return sub, true
}
