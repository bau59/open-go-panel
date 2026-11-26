package server

import (
	_ "embed"
	"net/http"
)

// All UI assets are embedded in the panel binary. Runtime never needs npm,
// external CDNs, or a writable frontend directory.

//go:embed assets/panel.css
var baseStyles string

//go:embed assets/htmx.min.js
var htmxScript string

//go:embed assets/panel.js
var panelScript string

//go:embed assets/editor-core.js
var editorCore string

//go:embed assets/editor-modes.js
var editorModes string

//go:embed assets/editor-addons.js
var editorAddons string

//go:embed assets/editor.js
var editorScript string

//go:embed assets/editor-core.css
var editorCoreCSS string

//go:embed assets/editor-dialog.css
var editorDialogCSS string

func registerUIAssets(mux *http.ServeMux) {
	assets := []struct {
		path, mime, content string
	}{
		{"/assets/panel.css", "text/css; charset=utf-8", baseStyles},
		{"/assets/htmx.min.js", "text/javascript; charset=utf-8", htmxScript},
		{"/assets/panel.js", "text/javascript; charset=utf-8", panelScript},
		{"/assets/editor.js", "text/javascript; charset=utf-8", editorCore + "\n;\n" + editorModes + "\n;\n" + editorAddons + "\n;\n" + editorScript},
		{"/assets/editor.css", "text/css; charset=utf-8", editorCoreCSS + "\n" + editorDialogCSS},
	}
	for _, asset := range assets {
		asset := asset
		mux.HandleFunc("GET "+asset.path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", asset.mime)
			w.Header().Set("Cache-Control", "public, max-age=300")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write([]byte(asset.content))
		})
	}
}
