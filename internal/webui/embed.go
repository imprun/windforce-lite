package webui

import "embed"

// FS contains the windforce-lite Web UI static assets.
//
//go:embed assets/*
var FS embed.FS
