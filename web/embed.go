// Package web embeds the browser UI.
package web

import "embed"

// FS holds the UI files served at /.
//
//go:embed index.html manifest.webmanifest css js icons
var FS embed.FS
