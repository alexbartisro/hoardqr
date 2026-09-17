// Package web embeds the built SvelteKit static site into the Go binary.
// `//go:embed` paths are relative to this file's own directory and can't
// traverse `..`, which is why this lives inside web/ rather than alongside
// cmd/hoardqr/main.go — `build/` only exists here after `npm run build`
// (the Dockerfile's frontend stage runs it before `go build`); a bare
// `go build` from a fresh clone fails until that's run once.
package web

import "embed"

//go:embed all:build
var Assets embed.FS
