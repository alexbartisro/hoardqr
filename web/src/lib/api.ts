// Mock client for the REST API surface in architecture plan §9. Every function here
// matches the real endpoint's signature and return/error shape so that Phase 3 only
// has to replace each function body with a fetch() call — nothing above this module
// should need to change. State lives in memory (cloned from fixtures.ts at import
// time) and resets on page reload; there is no persistence, matching a mock layer's
// job of standing in for a database that doesn't exist yet.

import { ITEMS, LOCATIONS, TAGS, USERS } from './fixtures';
import {
	ApiError,
	type Breadcrumb,
	type Item,
	type Location,
	type ResolveLocationResult,
	type ScanResult,
	type SearchSuggestion,
	type Tag,
	type User
} from './types';

let locations = structuredClone(LOCATIONS);
let items = structuredClone(ITEMS);
const tags = structuredClone(TAGS);
const users = structuredClone(USERS);

let nextLocationId = Math.max(...locations.map((l) => l.id)) + 1;
let nextItemId = Math.max(...items.map((i) => i.id)) + 1;
let nextTagId = Math.max(...tags.map((t) => t.id)) + 1;

/**
 * Simulates network latency so loading states get exercised during Phase 2, and —
 * just as importantly — clones the value so callers can never mutate the in-memory
 * store through a returned reference. Real fetch() always hands back freshly parsed
 * JSON; a mock that returns live references is more permissive than the thing it
 * stands in for, which papers over bugs (in-place sorts, optimistic edits) that
 * would only surface once Phase 3 swaps in real fetch() calls.
 */
function delay<T>(value: T, ms = 150): Promise<T> {
	return new Promise((resolve) => setTimeout(() => resolve(structuredClone(value)), ms));
}

/** Crockford-style misread normalization (§4): O→0, I/L→1, case-insensitive. */
function normalizeCode(code: string): string {
	return code
		.toUpperCase()
		.replace(/O/g, '0')
		.replace(/[IL]/g, '1');
}

function codesMatch(a: string, b: string): boolean {
	return normalizeCode(a) === normalizeCode(b);
}

function getBreadcrumb(locationId: number): Breadcrumb {
	const path: Breadcrumb = [];
	let current = locations.find((l) => l.id === locationId);
	while (current) {
		path.unshift({ id: current.id, name: current.name });
		current = current.parent_id != null ? locations.find((l) => l.id === current!.parent_id) : undefined;
	}
	return path;
}

function breadcrumbText(locationId: number): string {
	return getBreadcrumb(locationId)
		.map((l) => l.name)
		.join(' > ');
}

function descendantLocationIds(rootId: number): number[] {
	const ids = [rootId];
	for (let i = 0; i < ids.length; i++) {
		for (const loc of locations) {
			if (loc.parent_id === ids[i]) ids.push(loc.id);
		}
	}
	return ids;
}

// --- GET /api/locations?parent_id= ---
export async function getLocations(parentId?: number | null): Promise<Location[]> {
	const target = parentId ?? null;
	return delay(locations.filter((l) => l.parent_id === target));
}

// --- GET /api/locations/:id ---
export async function getLocation(id: number): Promise<{ location: Location; breadcrumb: Breadcrumb }> {
	const location = locations.find((l) => l.id === id);
	if (!location) throw new ApiError(404, `location ${id} not found`);
	return delay({ location, breadcrumb: getBreadcrumb(id) });
}

// --- GET /api/locations/:id/contents (recursive — §3) ---
export async function getLocationContents(
	id: number
): Promise<{ location: Location; breadcrumb: Breadcrumb; items: Item[] }> {
	const location = locations.find((l) => l.id === id);
	if (!location) throw new ApiError(404, `location ${id} not found`);
	const ids = new Set(descendantLocationIds(id));
	return delay({
		location,
		breadcrumb: getBreadcrumb(id),
		items: items.filter((i) => ids.has(i.location_id))
	});
}

// --- POST /api/locations ---
export async function createLocation(
	data: Pick<Location, 'name'> &
		Partial<Pick<Location, 'parent_id' | 'qr_token' | 'photo_url' | 'notes' | 'is_shared'>>
): Promise<Location> {
	if (data.qr_token && locations.some((l) => l.qr_token === data.qr_token)) {
		throw new ApiError(409, `qr_token ${data.qr_token} already in use`);
	}
	const location: Location = {
		id: nextLocationId++,
		parent_id: data.parent_id ?? null,
		owner_id: 1,
		is_shared: data.is_shared ?? true,
		name: data.name,
		qr_token: data.qr_token ?? generatePlainTextCode(),
		photo_url: data.photo_url ?? null,
		notes: data.notes ?? null,
		created_at: new Date().toISOString()
	};
	locations.push(location);
	return delay(location);
}

// --- PATCH /api/locations/:id ---
export async function updateLocation(id: number, patch: Partial<Location>): Promise<Location> {
	const location = locations.find((l) => l.id === id);
	if (!location) throw new ApiError(404, `location ${id} not found`);
	if (patch.qr_token && locations.some((l) => l.id !== id && l.qr_token === patch.qr_token)) {
		throw new ApiError(409, `qr_token ${patch.qr_token} already in use`);
	}
	Object.assign(location, patch);
	return delay(location);
}

// --- DELETE /api/locations/:id?force= (§3) ---
export async function deleteLocation(id: number, opts: { force?: boolean } = {}): Promise<void> {
	const location = locations.find((l) => l.id === id);
	if (!location) throw new ApiError(404, `location ${id} not found`);

	const directItems = items.filter((i) => i.location_id === id);
	const hasParent = location.parent_id !== null;

	if (!hasParent && directItems.length > 0 && !opts.force) {
		throw new ApiError(
			409,
			`location ${id} holds items directly and has no parent to promote them to — retry with force=true`
		);
	}

	if (hasParent) {
		for (const item of directItems) item.location_id = location.parent_id!;
	} else if (opts.force) {
		items = items.filter((i) => i.location_id !== id);
	}

	// Child locations become root-level, never deleted — parent_id ON DELETE SET NULL (§3).
	for (const child of locations) {
		if (child.parent_id === id) child.parent_id = null;
	}

	locations = locations.filter((l) => l.id !== id);
	return delay(undefined);
}

// --- GET /api/items?q=&tag=&location_id= ---
export async function getItems(params: { q?: string; tag?: string; location_id?: number } = {}): Promise<Item[]> {
	let result = items;
	if (params.location_id != null) {
		result = result.filter((i) => i.location_id === params.location_id);
	}
	if (params.tag) {
		result = result.filter((i) => i.tags.includes(params.tag!));
	}
	if (params.q) {
		const q = params.q.toLowerCase();
		result = result.filter((i) => i.name.toLowerCase().includes(q));
	}
	return delay(result);
}

// --- GET /api/items?sort=created_desc&page=&pageSize= — not in §9's table. The
// dashboard (Phase 2 step 4) shipped with no way to browse existing items short
// of already knowing what to search for; this is the fix — a paginated, newest-
// first feed. Add this route for real in Phase 3. breadcrumb here is deepest-
// location-first (the opposite of everywhere else in the app, which goes
// root-to-leaf) — for a scan-down-the-list glance, "which box" is the more
// useful headline than "which building".
export async function getRecentItems(
	params: { page?: number; pageSize?: number } = {}
): Promise<{ entries: { item: Item; breadcrumb: string }[]; total: number }> {
	const page = params.page ?? 1;
	const pageSize = params.pageSize ?? 10;
	const sorted = [...items].sort((a, b) => b.created_at.localeCompare(a.created_at));
	const start = (page - 1) * pageSize;
	const entries = sorted.slice(start, start + pageSize).map((item) => ({
		item,
		breadcrumb: getBreadcrumb(item.location_id)
			.map((l) => l.name)
			.reverse()
			.join(' > ')
	}));
	return delay({ entries, total: sorted.length });
}

// --- GET /api/tags — not in §9's table, but the Add Object form (Phase 2 step 4)
// needs a full tag list for selection; add this route for real in Phase 3. ---
export async function getTags(): Promise<Tag[]> {
	return delay(tags);
}

// --- POST /api/tags — also not in §9's table. Backs the Add Object tag
// autocomplete's create-if-missing behavior: typing a name with no existing
// match creates and persists it instead of just attaching a string to the one
// item. Idempotent by case-insensitive name so a duplicate request (e.g. the
// same new tag typed on two drafts) returns the tag already created rather
// than erroring or creating a second row — real endpoint should upsert the
// same way, matching Postgres's likely case-insensitive unique index on name.
export async function createTag(name: string): Promise<Tag> {
	const trimmed = name.trim();
	const existing = tags.find((t) => t.name.toLowerCase() === trimmed.toLowerCase());
	if (existing) return delay(existing);
	const tag: Tag = { id: nextTagId++, name: trimmed };
	tags.push(tag);
	return delay(tag);
}

// --- GET /api/items/:id — not in §9's table, but the item detail page (Phase 2 step
// 4) needs single-item fetch; add this route for real in Phase 3. ---
export async function getItemById(id: number): Promise<{ item: Item; breadcrumb: Breadcrumb }> {
	const item = items.find((i) => i.id === id);
	if (!item) throw new ApiError(404, `item ${id} not found`);
	return delay({ item, breadcrumb: getBreadcrumb(item.location_id) });
}

// --- POST /api/items ---
export async function createItem(
	data: Pick<Item, 'name' | 'location_id'> &
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
	if (!locations.some((l) => l.id === data.location_id)) {
		throw new ApiError(422, `location ${data.location_id} does not exist`);
	}
	const now = new Date().toISOString();
	const item: Item = {
		id: nextItemId++,
		location_id: data.location_id,
		owner_id: 1,
		is_shared: data.is_shared ?? true,
		name: data.name,
		description: data.description ?? null,
		quantity: data.quantity ?? 1,
		condition: data.condition ?? null,
		qr_token: data.qr_token ?? generatePlainTextCode(),
		photo_url: data.photo_url ?? null,
		purchase_date: data.purchase_date ?? null,
		purchase_price: data.purchase_price ?? null,
		receipt_url: data.receipt_url ?? null,
		custom_fields: data.custom_fields ?? {},
		created_at: now,
		updated_at: now,
		tags: data.tags ?? []
	};
	items.push(item);
	return delay(item);
}

// --- PATCH /api/items/:id ---
export async function updateItem(id: number, patch: Partial<Item>): Promise<Item> {
	const item = items.find((i) => i.id === id);
	if (!item) throw new ApiError(404, `item ${id} not found`);
	Object.assign(item, patch, { updated_at: new Date().toISOString() });
	return delay(item);
}

// --- DELETE /api/items/:id ---
export async function deleteItem(id: number): Promise<void> {
	if (!items.some((i) => i.id === id)) throw new ApiError(404, `item ${id} not found`);
	items = items.filter((i) => i.id !== id);
	return delay(undefined);
}

// --- GET /api/scan?code= (§6) ---
export async function scan(code: string): Promise<ScanResult> {
	const matchingItems = items.filter((i) => codesMatch(i.qr_token, code));
	if (matchingItems.length > 0) return delay({ kind: 'item', items: matchingItems });

	const location = locations.find((l) => codesMatch(l.qr_token, code));
	if (location) return delay({ kind: 'location', location });

	return delay({ kind: 'none' });
}

// --- GET /api/search/suggest?q= (§5) ---
export async function searchSuggest(query: string): Promise<SearchSuggestion[]> {
	if (!query.trim()) return delay([]);

	// One entry per entity, keeping its best score — a query can match both an
	// entity's code (exact) and its name (fuzzy); without this a single row could
	// appear twice with an identical `kind+id` key, which is a duplicate-key error
	// in Svelte's keyed {#each}, not just a cosmetic double-listing.
	const results = new Map<string, SearchSuggestion>();
	const upsert = (s: SearchSuggestion) => {
		const key = `${s.kind}:${s.id}`;
		const existing = results.get(key);
		if (!existing || s.score > existing.score) results.set(key, s);
	};

	for (const item of items) {
		if (codesMatch(item.qr_token, query)) {
			upsert({
				kind: 'item',
				id: item.id,
				name: item.name,
				score: 1,
				location_id: item.location_id,
				breadcrumb: breadcrumbText(item.location_id)
			});
		}
	}
	for (const location of locations) {
		if (codesMatch(location.qr_token, query)) {
			upsert({ kind: 'location', id: location.id, name: location.name, score: 1 });
		}
	}

	const q = query.toLowerCase();
	const fuzzyScore = (name: string): number => {
		const n = name.toLowerCase();
		if (n === q) return 1;
		if (n.startsWith(q)) return 0.8;
		if (n.includes(q)) return 0.5;
		return 0;
	};

	for (const item of items) {
		const score = fuzzyScore(item.name);
		if (score > 0) {
			upsert({
				kind: 'item',
				id: item.id,
				name: item.name,
				score,
				location_id: item.location_id,
				breadcrumb: breadcrumbText(item.location_id)
			});
		}
	}
	for (const location of locations) {
		const score = fuzzyScore(location.name);
		if (score > 0) upsert({ kind: 'location', id: location.id, name: location.name, score });
	}
	for (const tag of tags) {
		const score = fuzzyScore(tag.name);
		if (score > 0) upsert({ kind: 'tag', id: tag.id, name: tag.name, score });
	}

	const sorted = [...results.values()].sort((a, b) => b.score - a.score || a.name.localeCompare(b.name));
	return delay(sorted.slice(0, 10));
}

// --- GET /api/resolve-location?code= (§7) ---
export async function resolveLocation(code: string): Promise<ResolveLocationResult> {
	const result = await scan(code);
	if (result.kind === 'location') return { kind: 'location', location_id: result.location.id };
	if (result.kind === 'item') {
		if (result.items.length === 1) return { kind: 'location', location_id: result.items[0].location_id };
		return { kind: 'items', items: result.items };
	}
	return { kind: 'none' };
}

// --- GET /api/users, POST /api/users (admin only) ---
export async function getUsers(): Promise<User[]> {
	return delay(users);
}

export async function createUser(data: Pick<User, 'username' | 'role'>): Promise<User> {
	if (users.some((u) => u.username === data.username)) {
		throw new ApiError(409, `username ${data.username} already exists`);
	}
	const user: User = { id: users.length + 1, username: data.username, role: data.role, created_at: new Date().toISOString() };
	users.push(user);
	return delay(user);
}

// --- POST /api/login, /api/logout — only active when AUTH_REQUIRED=true (§10) ---
export async function login(username: string, _password: string): Promise<User> {
	const user = users.find((u) => u.username === username);
	if (!user) throw new ApiError(401, 'invalid credentials');
	return delay(user);
}

export async function logout(): Promise<void> {
	return delay(undefined);
}

// --- GET /healthz ---
export async function healthz(): Promise<{ ok: true }> {
	return delay({ ok: true });
}

/** Generated plain-text code (§4): 6-char Crockford Base32, ambiguous chars excluded. */
function generatePlainTextCode(): string {
	const alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'; // no I, L, O, U
	let code = '';
	for (let i = 0; i < 6; i++) {
		code += alphabet[Math.floor(Math.random() * alphabet.length)];
	}
	return code;
}
