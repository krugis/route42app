// Package webui embeds the optional single-page management console served
// by the gateway at "/". The UI is plain HTML/CSS/JS with zero build step
// and zero runtime dependencies, so the single-binary story is unchanged:
// everything ships inside the executable via go:embed. The gateway remains
// fully usable without it (CLI + HTTP API); set server.ui: false to turn
// the console off entirely.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var static embed.FS

// Handler serves the embedded console assets. index.html is served at "/"
// by the standard file-server index behavior.
func Handler() http.Handler {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		// Unreachable: "static" is embedded above at compile time.
		panic("webui: embedded assets missing: " + err.Error())
	}
	return http.FileServerFS(sub)
}
