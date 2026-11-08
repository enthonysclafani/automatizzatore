// Package spec embeds the public JSON Schema of the automation manifest.
//
// The schema file is the source of truth for the manifest format: editors,
// AI builders and the validator all read the same file.
package spec

import _ "embed"

// SchemaV1 is the JSON Schema for manifests with apiVersion "pendolo/v1".
//
//go:embed pendolo-v1.schema.json
var SchemaV1 []byte

// SchemaV1URL is the $id of SchemaV1.
const SchemaV1URL = "https://github.com/enthonysclafani/automatizzatore/spec/pendolo-v1.schema.json"
