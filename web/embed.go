package web

import "embed"

// Files is the embedded native browser UI.
//
//go:embed index.html app.js style.css
var Files embed.FS
