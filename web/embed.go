// Package web embeds the built frontend into the service binary.
package web

import (
	"embed"
	"io/fs"
)

// dist is web/dist: dist/.keep is in Git, so the service builds before the frontend is built.
//
//go:embed all:dist
var dist embed.FS

// App returns the built application, web/dist/app. It is empty until task build:web runs.
func App() (fs.FS, error) {
	return fs.Sub(dist, "dist/app")
}
