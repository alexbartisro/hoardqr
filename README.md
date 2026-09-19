# HoardQR

A self-hosted home inventory system: items live in arbitrarily-nested storage locations (`Balcony → Storage Cabinet → Shelf 2 → Box 4`), each item or location can carry a QR/barcode/plain-text code, and scanning one from your phone resolves straight to it. Unified autocomplete search across items, locations, and tags. Optional multi-user sharing. Also queryable from chat via MCP.

Runs as a couple of Docker containers plus Postgres — built to sit alongside a homelab's existing self-hosted stack.

## ⚠️ Vibe-coded

This project is built primarily by prompting AI coding assistants (Claude Code), not hand-written line by line. Expect the usual tradeoffs that come with that: iteration speed over architectural purity in places, and code that hasn't necessarily been read as closely as it would be in a professionally-reviewed codebase. It's a personal homelab tool, not production software with guarantees. Use, fork, or judge accordingly.

## Status

Pre-alpha, but deployable — the Go backend, real Postgres schema, and MCP server are in place on the `dev` branch (a tagged `v0.0.1` release on `main` covers the earlier mocked-frontend-only milestone). Still under active development; expect rough edges and schema/API changes without migration paths. See the commit history for current progress; there's no changelog yet.

## Stack

Go backend (`chi`, `pgx`/`sqlc`), Postgres 16 (`pg_trgm` for search), Svelte 5 + SvelteKit frontend (Tailwind v4, shadcn-svelte), PWA camera scanning, client-side OCR, and an MCP server for chat-based queries. The Go binary embeds the built frontend via `go:embed`, so production is one binary plus Postgres.

## Running it

```
cp .env.example .env   # fill in real values
docker compose up --build
```

`docker-compose.yml` is intentionally generic — no reverse proxy, no fixed domain. If you front it with Traefik, Caddy, nginx, etc., add your own `docker-compose.override.yml` (gitignored) with the labels/networks your setup needs; Compose merges it automatically.

**Data lives in `./uploads` and `./pgdata`** next to `docker-compose.yml` by default. If you keep all your containers' data under one shared path instead (e.g. `/srv/docker-data/...`), set `HOST_UPLOADS_DIR` and `HOST_PGDATA_DIR` in `.env` to point there — see `.env.example`.

**If your override file also sets `DATABASE_URL`**, make sure it keeps the `?sslmode=disable` suffix the shipped `docker-compose.yml` uses — an override completely replaces that environment value rather than adding to it, and dropping the suffix reintroduces a real startup failure (see below). Also remember that pulling a new image doesn't update your compose files — if you maintain your deployment outside a git checkout of this repo, re-diff your copy against `docker-compose.yml` after pulling.

**Why `sslmode=disable` is there at all**, since it looks like a security downgrade and isn't one: `hoardqr-db` has no SSL configured (normal for a container that's only ever reachable over the private Compose network, not the public internet), and nearly every Postgres client defaults to trying SSL and silently falling back to plaintext when a server doesn't offer it. Go's `lib/pq` — used internally by the migration step, not by the app's own request-serving connection pool — is the one exception: its documented default is to *require* SSL and fail outright instead of falling back. Without `sslmode=disable`, `docker logs -f hoardqr` shows the app connecting fine and then migrations failing forever with `pq: SSL is not enabled on the server`.

A healthy startup looks like this in `docker logs -f hoardqr`:
```
database connected
migrations up to date
hoardqr serve listening
```
If you see `database connected` immediately followed by `running migrations` failing on a loop, that's this — check `DATABASE_URL` first.

## License

[AGPLv3](./LICENSE) — see `LICENSE` for the full text. Self-hosting your own modified copy is unrestricted; running a modified copy as a network service for others requires publishing that modified source in turn.
