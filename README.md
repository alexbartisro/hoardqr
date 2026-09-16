# HoardQR

A self-hosted home inventory system: items live in arbitrarily-nested storage locations (`Balcony → Storage Cabinet → Shelf 2 → Box 4`), each item or location can carry a QR/barcode/plain-text code, and scanning one from your phone resolves straight to it. Unified autocomplete search across items, locations, and tags. Optional multi-user sharing. Also queryable from chat via MCP.

Runs as a couple of Docker containers plus Postgres — built to sit alongside a homelab's existing self-hosted stack.

## ⚠️ Vibe-coded

This project is built primarily by prompting AI coding assistants (Claude Code), not hand-written line by line. Expect the usual tradeoffs that come with that: iteration speed over architectural purity in places, and code that hasn't necessarily been read as closely as it would be in a professionally-reviewed codebase. It's a personal homelab tool, not production software with guarantees. Use, fork, or judge accordingly.

## Status

Pre-alpha — currently being scaffolded. Nothing is deployable yet. See the commit history and issues for current progress; there's no changelog or release yet.

## Stack

Go backend (`chi`, `pgx`/`sqlc`), Postgres 16 (`pg_trgm` for search), Svelte 5 + SvelteKit frontend (Tailwind v4, shadcn-svelte), PWA camera scanning, client-side OCR, and an MCP server for chat-based queries. The Go binary embeds the built frontend via `go:embed`, so production is one binary plus Postgres.

## Running it

```
cp .env.example .env   # fill in real values
docker compose up --build
```

`docker-compose.yml` is intentionally generic — no reverse proxy, no fixed domain. If you front it with Traefik, Caddy, nginx, etc., add your own `docker-compose.override.yml` (gitignored) with the labels/networks your setup needs; Compose merges it automatically.

## License

[AGPLv3](./LICENSE) — see `LICENSE` for the full text. Self-hosting your own modified copy is unrestricted; running a modified copy as a network service for others requires publishing that modified source in turn.
