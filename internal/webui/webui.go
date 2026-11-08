// Package webui embeds the built dashboard. `make web` builds web/ into
// internal/webui/dist; without a build the server shows a short notice.
package webui

import "embed"

// Dist holds the files produced by `npm run build` in web/.
//
//go:embed all:dist
var Dist embed.FS
