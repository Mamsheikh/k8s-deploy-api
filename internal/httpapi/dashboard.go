package httpapi

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

//go:embed web/index.html
var dashboardHTML []byte

//go:embed web/app.css
var dashboardCSS []byte

//go:embed web/app.js
var dashboardJS []byte

func dashboardAsset(content []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
	}
}
