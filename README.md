# HoardQR

A self-hosted home inventory system: catalog everything you own against a storage location that can be nested as deep as your house actually is (`Balcony → Storage Cabinet → Shelf 2 → Box 4`), stick a code on the box, and scan it later to land straight on what's inside. Also queryable from chat via MCP, so "where's my multimeter?" doesn't require opening an app at all.

Runs as a couple of Docker containers plus Postgres — built to sit alongside a homelab's existing self-hosted stack, behind whatever reverse proxy you already use.

## ⚠️ Vibe-coded

This project is built primarily by prompting AI coding assistants (Claude Code), not hand-written line by line. Expect the usual tradeoffs that come with that: iteration speed over architectural purity in places, and code that hasn't necessarily been read as closely as it would be in a professionally-reviewed codebase. It's a personal homelab tool, not production software with guarantees. Use, fork, or judge accordingly.

## Status

Pre-alpha, but deployable — running on the author's own homelab. The Go backend, real Postgres schema, full web UI, and MCP server are all in place on the `dev` branch (a tagged `v0.0.1` release on `main` covers an earlier mocked-frontend-only milestone). Still under active development; expect rough edges and schema/API changes without migration paths. See the commit history for current progress; there's no changelog yet.

## Features

**Location tree.** Boxes, shelves, cupboards, rooms — all the same kind of thing, nested as deep as you want. A location can hold both items and other locations at once.

**Items with real metadata.** Name, description, quantity, condition, tags (autocomplete, create-on-the-fly), a photo, purchase date and price, and a free-form custom-fields bucket for anything else — none of it required except a name and where it lives.

**Three ways a code ends up on something**, all stored in the same field and resolved the same way at scan/search time:
- **Generate a QR code or Code128 barcode** — rendered client-side straight from a random token, printable on a label sheet.
- **Adopt an existing retail barcode** — scan the UPC/EAN already on the box while creating the item and that becomes its code. Nothing to print; scanning it again later resolves straight back.
- **Generate a short plain-text code** (e.g. `K7XQ2M`) for when there's no label printer around — Crockford Base32, ambiguous characters excluded, common OCR/handwriting misreads normalized before comparison so a hand-written sticky note still resolves correctly.

Codes are editable after the fact (fixing a mis-printed or mis-written one is just a normal edit), and printable label sheets are plain web pages — any printer your OS already knows about works, no dedicated label-printer integration needed.

**Camera scanning, from a phone.** Install HoardQR to your home screen as a PWA, open `/scan`, point the camera at a code. One match jumps straight to it; a location match shows everything inside it; no match offers to create something new right there, pre-filled with the scanned code. Several items sharing the same code (a common product kept in different places) is designed to show a short picker instead of guessing — implemented, but not yet confirmed against a real duplicate-code scan on physical hardware.

**Client-side OCR for plain-text labels.** No camera-readable code at all? Read the label with Tesseract.js instead (English + Romanian traineddata, fully offline, nothing ever leaves the phone) — the recognized text feeds the same search used everywhere else, so it doesn't matter whether what got read was a code or a name.

**One shared Location Picker component**, used everywhere a location needs to be assigned (adding an object, adding storage, moving something): type a name, scan a code, or read a label — same three options every time.

**Unified fuzzy search.** One search bar, one query, ranks items/locations/tags together with typo tolerance (Postgres `pg_trgm`) — an exact code match always wins first. The same endpoint backs the web search bar, the Location Picker, and the MCP `find_items` tool.

**A real dashboard**, not just a form: quick "Add Object"/"Add Storage" shortcuts, a preview of your root-level storage locations, and a newest-first preview of recently added items — each with a "See all" link once there's more than the preview shows.

**Two dedicated browse pages**: `/locations` is your entire storage tree as an expandable list (fetches children lazily as you open each one — nothing loads until you ask for it); `/items` is every object you own, paginated, newest first, each with its full location breadcrumb. A persistent bottom tab bar (Home / Storage / Objects) gets you to either from anywhere.

**Photo uploads**, compressed and stripped of EXIF/GPS entirely client-side before they ever reach the server (resized to 1600px, re-encoded to WebP) — a multi-megabyte phone photo typically shrinks to under 300KB before upload even starts.

**Queryable and editable from any MCP-capable chat client** — see [MCP server](#mcp-server) below.

**Installable as a PWA** — add it to your phone's home screen and it behaves like a native app shell (requires HTTPS; see [Deployment notes](#deployment-notes)).

### What's designed but not built yet

Being upfront about the gap between the architecture plan and what's actually running, since a self-hoster deciding whether this fits should know:

- **Auth and multi-user sharing exist in the schema and the plan, not in the running code.** There's no login, no user accounts, no session — every request is served as a single implicit identity, and every row is visible to anyone who can reach the instance. `AUTH_REQUIRED` in `.env.example` is a reserved config value the backend reads but doesn't currently act on anywhere. If you need this instance private, put it behind your own auth layer (a reverse-proxy `forward-auth`, a VPN, etc.) — HoardQR itself won't gate access.
- **The audit log table exists but is barely used.** Only the MCP `move_item` tool writes to it; nothing in the web UI does, and there's no page to view it even if it were populated.
- No CSV import/export, no bulk move/edit/print for multiple items at once, no expiring-warranty or other notifications. None of these are implemented.

## Use cases

- **Buy something, catalog it on the spot.** Open the dashboard, "Add Object," point the Location Picker at where it's going (type, scan the shelf's code, or just point the camera at whatever's already written on the shelf), name it, done. Print a label for it later if it doesn't already have a barcode.
- **"Where did I put the multimeter?"** Type it into search, or just ask whatever chat client you've pointed at the MCP server — same answer either way, with the full location breadcrumb.
- **Reorganizing.** Moving a box of winter clothes from the garage to the attic: scan the box's code on `/scan`, or tell the chat client to move it — either way the system of record updates immediately, not just your memory of where it went.
- **A relative's old electronics show up in a box.** Half of them already have a manufacturer barcode on them — scan it while adding each one instead of generating and printing a new code; the retail barcode just becomes the item's code.
- **You inherited a labeled box with no scannable code, just handwriting.** Read the label with the phone camera (OCR) instead of retyping it.
- **Browsing rather than searching.** `/locations` to see the whole tree of where things live, `/items` to scroll everything you've cataloged newest-first — for when you don't know exactly what you're looking for yet, just want to look around.
- **Ask an LLM to do the cataloging for you.** Because the MCP tools are the same operations the web UI uses, a capable chat client can add, find, and move things on your behalf conversationally — "I just bought a new drill, put it in the garage toolbox" — without you touching the app at all.

## MCP server

Runs as a second process from the same binary (`hoardqr mcp`), separate port, Streamable HTTP transport at `/mcp`. Point any MCP-capable client at `http://<host>:8081/mcp` with the bearer token you set in `MCP_API_TOKEN`.

Six tools, the same operations the web UI itself uses:

| Tool | Does |
|---|---|
| `find_items(query)` | Search by name, tag, or code |
| `where_is(name)` | Resolve an item to its full location breadcrumb |
| `list_contents(location)` | Everything stored in a location, recursively |
| `add_item(name, location, quantity?)` | Catalog a new object |
| `add_location(name, parent?)` | Create a new storage location |
| `move_item(item, new_location)` | Move an existing item |

If two items or locations share a similar-enough name, the name-resolving tools refuse to guess and ask you to be more specific, rather than silently acting on the wrong one. If `MCP_API_TOKEN` is left unset, the server runs unauthenticated with a startup warning — fine on a LAN-only deploy, not recommended if this port is ever exposed further than that.

## Stack

Go backend (`chi`, `pgx`/`sqlc`, `golang-migrate`), Postgres 16 (`pg_trgm` for fuzzy search), Svelte 5 + SvelteKit frontend (Tailwind v4, shadcn-svelte), PWA camera scanning (`html5-qrcode`), client-side OCR (`Tesseract.js`), client-side photo compression (`browser-image-compression`), and an MCP server (`modelcontextprotocol/go-sdk`) for chat-based queries. The Go binary embeds the built frontend via `go:embed`, so production is one binary plus Postgres — no Node, no separate frontend server, no CORS to configure.

## Running it

```
cp .env.example .env   # fill in real values
docker compose up --build
```

This starts three containers: `hoardqr` (the web app, port 8080), `hoardqr-mcp` (the MCP server, port 8081), and `hoardqr-db` (Postgres). `docker-compose.yml` is intentionally generic — no reverse proxy, no fixed domain. If you front it with Traefik, Caddy, nginx, etc., add your own `docker-compose.override.yml` (gitignored) with the labels/networks your setup needs; Compose merges it in automatically.

A healthy startup looks like this in `docker logs -f hoardqr`:
```
database connected
migrations up to date
hoardqr serve listening
```

### Configuration

Every variable below lives in `.env.example` with its own inline comment — this table is the quick-reference version.

| Variable | Default | What it's for |
|---|---|---|
| `DB_PASSWORD` | — (required) | Postgres password; `docker-compose.yml` builds every container's `DATABASE_URL` from this |
| `SESSION_SECRET` | unset | Reserved for the session auth described above; not currently exercised by any running code path — safe to leave unset |
| `MCP_API_TOKEN` | unset (unauthenticated + warning) | Bearer token the MCP server checks on every request |
| `AUTH_REQUIRED` | `false` | Reserved — read into config but not currently acted on anywhere (see [What's designed but not built yet](#whats-designed-but-not-built-yet)) |
| `HOST_UPLOADS_DIR` | `./uploads` | Host-side path for uploaded photos — set this if you keep all your containers' data under one shared path instead |
| `HOST_PGDATA_DIR` | `./pgdata` | Host-side path for the Postgres data directory, same idea |
| `UPLOAD_DIR` | `/data/uploads` | Only relevant running the bare binary directly (not Docker) — the container-internal upload path is fixed |
| `DATABASE_URL` | — | Only relevant running the bare binary directly; Docker builds this itself from `DB_PASSWORD` |

**Data lives in `./uploads` and `./pgdata`** next to `docker-compose.yml` by default (see `HOST_UPLOADS_DIR`/`HOST_PGDATA_DIR` above to change that). Both are plain directories — any backup tool that already watches your other containers' data (Duplicati, Kopia, etc.) picks them up with no extra config.

### Deployment notes

**PWA install and the service worker both require HTTPS** (or `localhost`) — this is a browser spec requirement, not something HoardQR's own config controls. It'll work fine behind Traefik/Caddy with a real certificate; it will *not* work at a bare LAN IP over plain HTTP, regardless of how the manifest is configured.

**If your override file also sets `DATABASE_URL`**, make sure it keeps the `?sslmode=disable` suffix the shipped `docker-compose.yml` uses — an override completely replaces that environment value rather than adding to it, and dropping the suffix reintroduces a real startup failure (see below). Also remember that pulling a new image doesn't update your compose files — if you maintain your deployment outside a git checkout of this repo, re-diff your copy against `docker-compose.yml` after pulling.

**Why `sslmode=disable` is there at all**, since it looks like a security downgrade and isn't one: `hoardqr-db` has no SSL configured (normal for a container that's only ever reachable over the private Compose network, not the public internet), and nearly every Postgres client defaults to trying SSL and silently falling back to plaintext when a server doesn't offer it. Go's `lib/pq` — used internally by the migration step, not by the app's own request-serving connection pool — is the one exception: its documented default is to *require* SSL and fail outright instead of falling back. Without `sslmode=disable`, `docker logs -f hoardqr` shows the app connecting fine and then migrations failing forever with `pq: SSL is not enabled on the server`. If you see `database connected` immediately followed by `running migrations` failing on a loop, that's this — check `DATABASE_URL` first.

**Photo URLs are unauthenticated by design** — served as plain static files from `/uploads/`, not gated behind whatever access control you put in front of the app. Fine as long as those URLs never leave the app; worth knowing before assuming every part of the app is equally private.

## License

[AGPLv3](./LICENSE) — see `LICENSE` for the full text. Self-hosting your own modified copy is unrestricted; running a modified copy as a network service for others requires publishing that modified source in turn.
