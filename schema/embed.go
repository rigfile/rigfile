// Package schema embeds the Rigfile manifest JSON Schema so the CLI can validate
// manifests without reading anything from disk at run time.
package schema

import _ "embed"

// ManifestV1 is schema/rigfile.v1.json (apiVersion rigfile.dev/v1).
//
//go:embed rigfile.v1.json
var ManifestV1 []byte
