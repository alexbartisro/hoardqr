// Real REST client for the Go backend built in Phase 3 — every function here
// used to be a mock (see git history) with the same signature and return
// shape, matching architecture plan §9 (plus the extra routes documented in
// CLAUDE.md's "Mock API surface beyond §9"). Nothing above this module
// needed to change: every route/component still just calls these functions.
//
// Requests use relative paths (`/api/...`), not an absolute base URL — in
// production the SvelteKit build is embedded into the same Go binary that
// serves the API (architecture plan §2: same origin, no CORS to maintain),
// and in dev, vite.config.ts proxies `/api` and `/healthz` to the Go
// server's port so `npm run dev` works against a real local backend too.

import { USERS } from './fixtures';
import { ApiError, type Breadcrumb, type Item, type Location, type Storage, type ResolveStorageResult, type ScanResult, type SearchSuggestion, type Tag, type User } from './types';

async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
	// A FormData body (photo upload) must NOT get a Content-Type set here —
	// the browser needs to set its own `multipart/form-data; boundary=...`,
	// which it only does when the header is left absent.
	const isFormData = init.body instanceof FormData;
	const res = await fetch(path, {
		...init,
		headers: init.body && !isFormData ? { 'Content-Type': 'application/json', ...init.headers } : init.headers
	});
	if (!res.ok) {
		let message = res.statusText;
		try {
			const body = await res.json();
			if (body && typeof body.error === 'string') message = body.error;
		} catch {
			// non-JSON error body (e.g. a proxy error page) — fall back to statusText
		}
		throw new ApiError(res.status, message);
	}
	if (res.status === 204) return undefined as T;
	return (await res.json()) as T;
}

/** Appends only the params that are actually set — omitted, not sent as `key=`. */
function queryString(params: Record<string, string | number | boolean | undefined | null>): string {
	const usp = new URLSearchParams();
	for (const [key, value] of Object.entries(params)) {
		if (value !== undefined && value !== null && value !== '') usp.set(key, String(value));
	}
	const qs = usp.toString();
	return qs ? `?${qs}` : '';
}

// --- GET /api/storages?parent_id= ---
export async function getStorages(parentId?: number | null): Promise<Storage[]> {
	return apiFetch(`/api/storages${queryString({ parent_id: parentId })}`);
}

// --- GET /api/storages/:id ---
export async function getStorage(
	id: number
): Promise<{ storage: Storage; breadcrumb: Breadcrumb; location: Pick<Location, 'id' | 'name'> | null }> {
	return apiFetch(`/api/storages/${id}`);
}

// --- GET /api/storages/:id/contents (recursive — §3) ---
export async function getStorageContents(
	id: number
): Promise<{
	storage: Storage;
	breadcrumb: Breadcrumb;
	location: Pick<Location, 'id' | 'name'> | null;
	items: Item[];
}> {
	return apiFetch(`/api/storages/${id}/contents`);
}

// --- POST /api/storages ---
export async function createStorage(
	data: Pick<Storage, 'name'> &
		Partial<Pick<Storage, 'parent_id' | 'location_id' | 'qr_token' | 'photo_url' | 'notes' | 'is_shared'>>
): Promise<Storage> {
	return apiFetch('/api/storages', { method: 'POST', body: JSON.stringify(data) });
}

// --- PATCH /api/storages/:id ---
export async function updateStorage(id: number, patch: Partial<Storage>): Promise<Storage> {
	return apiFetch(`/api/storages/${id}`, { method: 'PATCH', body: JSON.stringify(patch) });
}

// --- DELETE /api/storages/:id?force= (§3) ---
export async function deleteStorage(id: number, opts: { force?: boolean } = {}): Promise<void> {
	return apiFetch(`/api/storages/${id}${queryString({ force: opts.force })}`, { method: 'DELETE' });
}

// --- GET /api/locations ---
export async function getLocations(): Promise<Location[]> {
	return apiFetch('/api/locations');
}

// --- GET /api/locations/:id — includes its assigned root storages. ---
export async function getLocation(id: number): Promise<{ location: Location; storages: Storage[] }> {
	return apiFetch(`/api/locations/${id}`);
}

// --- POST /api/locations ---
export async function createLocation(data: Pick<Location, 'name'>): Promise<Location> {
	return apiFetch('/api/locations', { method: 'POST', body: JSON.stringify(data) });
}

// --- PATCH /api/locations/:id — name only, a location's one editable field. ---
export async function updateLocation(id: number, patch: Pick<Location, 'name'>): Promise<Location> {
	return apiFetch(`/api/locations/${id}`, { method: 'PATCH', body: JSON.stringify(patch) });
}

// --- DELETE /api/locations/:id?force= ---
export async function deleteLocation(id: number, opts: { force?: boolean } = {}): Promise<void> {
	return apiFetch(`/api/locations/${id}${queryString({ force: opts.force })}`, { method: 'DELETE' });
}

// --- GET /api/items?q=&tag=&storage_id= ---
export async function getItems(params: { q?: string; tag?: string; storage_id?: number } = {}): Promise<Item[]> {
	return apiFetch(`/api/items${queryString(params)}`);
}

// --- GET /api/items?sort=created_desc&page=&pageSize= — not in §9's table. The
// dashboard's newest-first feed (see CLAUDE.md's "Mock API surface beyond §9"). ---
export async function getRecentItems(
	params: { page?: number; pageSize?: number } = {}
): Promise<{ entries: { item: Item; breadcrumb: string }[]; total: number }> {
	return apiFetch(`/api/items${queryString({ sort: 'created_desc', ...params })}`);
}

// --- GET /api/tags — not in §9's table (see CLAUDE.md). ---
export async function getTags(): Promise<Tag[]> {
	return apiFetch('/api/tags');
}

// --- POST /api/tags — also not in §9's table. Backs the Add Object tag
// autocomplete's create-if-missing behavior; the backend upserts by
// case-insensitive name (see internal/api/tags.go), same idempotency the
// mock had. ---
export async function createTag(name: string): Promise<Tag> {
	return apiFetch('/api/tags', { method: 'POST', body: JSON.stringify({ name }) });
}

// --- POST /api/photos — not in §9's table (see CLAUDE.md); the architecture
// plan designs the storage/serving shape in §12 but not this endpoint
// itself. Takes an already-client-compressed File/Blob (see
// $lib/components/photo-upload.svelte, which runs browser-image-compression
// before calling this) and returns the URL to set as an item's or
// storage's photo_url via the normal create/update calls — this endpoint
// never touches those tables itself. ---
export async function uploadPhoto(file: Blob): Promise<{ photo_url: string }> {
	const formData = new FormData();
	formData.set('photo', file, 'photo');
	return apiFetch('/api/photos', { method: 'POST', body: formData });
}

// --- GET /api/items/:id — not in §9's table (see CLAUDE.md). ---
export async function getItemById(
	id: number
): Promise<{ item: Item; breadcrumb: Breadcrumb; location: Pick<Location, 'id' | 'name'> | null }> {
	return apiFetch(`/api/items/${id}`);
}

// --- POST /api/items ---
export async function createItem(
	data: Pick<Item, 'name' | 'storage_id'> &
		Partial<
			Pick<
				Item,
				| 'description'
				| 'quantity'
				| 'condition'
				| 'qr_token'
				| 'photo_url'
				| 'purchase_date'
				| 'purchase_price'
				| 'receipt_url'
				| 'custom_fields'
				| 'is_shared'
				| 'tags'
			>
		>
): Promise<Item> {
	return apiFetch('/api/items', { method: 'POST', body: JSON.stringify(data) });
}

// --- PATCH /api/items/:id ---
export async function updateItem(id: number, patch: Partial<Item>): Promise<Item> {
	return apiFetch(`/api/items/${id}`, { method: 'PATCH', body: JSON.stringify(patch) });
}

// --- DELETE /api/items/:id ---
export async function deleteItem(id: number): Promise<void> {
	return apiFetch(`/api/items/${id}`, { method: 'DELETE' });
}

// --- GET /api/scan?code= (§6) ---
export async function scan(code: string): Promise<ScanResult> {
	return apiFetch(`/api/scan${queryString({ code })}`);
}

// --- GET /api/search/suggest?q= (§5) ---
export async function searchSuggest(query: string): Promise<SearchSuggestion[]> {
	// Short-circuit client-side rather than round-tripping an empty query — the
	// backend already returns [] for one too, but every caller here is a
	// debounced-as-you-type field, so skipping the request outright avoids a
	// network round-trip on every "field just got focused" empty state.
	if (!query.trim()) return [];
	return apiFetch(`/api/search/suggest${queryString({ q: query })}`);
}

// --- GET /api/resolve-storage?code= (§7) ---
export async function resolveStorage(code: string): Promise<ResolveStorageResult> {
	return apiFetch(`/api/resolve-storage${queryString({ code })}`);
}

// --- GET /api/users, POST /api/users, POST /api/login, /api/logout ---
// Auth is deferred indefinitely (decided 2026-09-18 — see CLAUDE.md): no
// backend route exists for any of these yet, and none is currently reachable
// from the UI's actual navigation (the login form has no entry point either
// — CLAUDE.md notes this too). Left mocked against the same fixture data
// they always used rather than wired to fetch() calls that would just 404,
// so the login form and any future user-management screen still work
// against something until real auth exists.
const users = structuredClone(USERS);

function delay<T>(value: T, ms = 150): Promise<T> {
	return new Promise((resolve) => setTimeout(() => resolve(structuredClone(value)), ms));
}

export async function getUsers(): Promise<User[]> {
	return delay(users);
}

export async function createUser(data: Pick<User, 'username' | 'role'>): Promise<User> {
	if (users.some((u) => u.username === data.username)) {
		throw new ApiError(409, `username ${data.username} already exists`);
	}
	const user: User = {
		id: users.length + 1,
		username: data.username,
		role: data.role,
		created_at: new Date().toISOString()
	};
	users.push(user);
	return delay(user);
}

export async function login(username: string, _password: string): Promise<User> {
	const user = users.find((u) => u.username === username);
	if (!user) throw new ApiError(401, 'invalid credentials');
	return delay(user);
}

export async function logout(): Promise<void> {
	return delay(undefined);
}

// --- GET /healthz ---
// Not apiFetch: /healthz responds with a plain "ok" body, not JSON (see
// cmd/hoardqr/main.go) — apiFetch's res.json() would throw on it.
export async function healthz(): Promise<{ ok: true }> {
	const res = await fetch('/healthz');
	if (!res.ok) throw new ApiError(res.status, res.statusText);
	return { ok: true };
}
