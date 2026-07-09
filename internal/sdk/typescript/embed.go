package windforceclient

import "embed"

// Files contains the vendored TypeScript author SDK injected into app sources.
//
//go:embed index.ts index.d.ts package.json
var Files embed.FS
