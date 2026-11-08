// Package docs embeds the user documentation the AI builder learns from,
// so it always describes the format this binary runs.
package docs

import _ "embed"

// Manifest is docs/manifest.md: the automation format.
//
//go:embed manifest.md
var Manifest string

// Connectors is docs/connectors.md: connections and their actions.
//
//go:embed connectors.md
var Connectors string
