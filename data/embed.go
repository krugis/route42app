// Package data embeds the versioned model catalog snapshot so the
// route42 binary works with zero configuration. A newer snapshot can be
// supplied at runtime via the db/catalog config (see internal/catalog).
package data

import _ "embed"

// CatalogJSON is the embedded model catalog snapshot.
//
//go:embed catalog.json
var CatalogJSON []byte
