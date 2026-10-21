// Package web embeds the browser UI into the server binary.
package web

import "embed"

//go:embed index.html app.js style.css sha256.js hash-core.js hash-worker.js logic.js manifest.webmanifest sw.js icons
var Files embed.FS
