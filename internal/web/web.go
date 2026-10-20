// Package web embeds the Viewer web app into the server binary. It
// is the go:embed shim only: assets in, an fs.FS out, no other internal imports
// (ARCHITECTURE.md §4 row "web").
package web

import (
	"embed"
	"io/fs"
)

//go:embed app-dist
var appFS embed.FS

// AppAssets returns the embedded React bundle ready for http.FileServerFS. A
// production build lives in the ignored production subtree; standalone clones
// use the tracked, self-consistent placeholder at the app-dist root.
func AppAssets() fs.FS {
	assets, err := fs.Sub(appFS, "app-dist")
	if err != nil {
		// Unreachable: the subdirectory is compiled into the binary.
		panic(err)
	}
	if _, err := fs.Stat(assets, "production/index.html"); err == nil {
		production, err := fs.Sub(assets, "production")
		if err != nil {
			panic(err)
		}
		return production
	}
	return assets
}
