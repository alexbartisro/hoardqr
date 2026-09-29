// Field names match the Postgres schema (architecture plan §3) exactly, snake_case
// included, since that's what the real JSON API will return — no translation layer
// to maintain when Phase 3 swaps lib/api.ts's mock bodies for real fetch() calls.

export interface Storage {
	id: number;
	parent_id: number | null;
	owner_id: number | null;
	is_shared: boolean;
	name: string;
	qr_token: string;
	photo_url: string | null;
	notes: string | null;
	created_at: string;
	/** Real column — non-null only on a root storage (nested storages inherit
	 * their location transitively via the breadcrumb instead, §3). */
	location_id: number | null;
	/** Only ever populated by getStorages() (a LEFT JOIN server-side) — every
	 * other call returns location_id alone. */
	location_name?: string | null;
}

/** A flat, non-nested physical property (House, Garage, Parent's House) that
 * a root-level Storage can optionally belong to (architecture plan §3). */
export interface Location {
	id: number;
	owner_id: number | null;
	is_shared: boolean;
	name: string;
	created_at: string;
}

export interface Item {
	id: number;
	storage_id: number;
	owner_id: number | null;
	is_shared: boolean;
	name: string;
	description: string | null;
	quantity: number;
	condition: string | null;
	qr_token: string;
	photo_url: string | null;
	purchase_date: string | null;
	purchase_price: number | null;
	custom_fields: Record<string, unknown>;
	created_at: string;
	updated_at: string;
	/** Joined for display convenience — not a schema column. */
	tags: string[];
}

export interface Tag {
	id: number;
	name: string;
}

export interface User {
	id: number;
	username: string;
	role: 'admin' | 'user';
	created_at: string;
}

/** Root-to-node path, per the recursive breadcrumb query in architecture plan §3. */
export type Breadcrumb = Pick<Storage, 'id' | 'name'>[];

export interface SearchSuggestion {
	kind: 'item' | 'storage' | 'tag';
	id: number;
	name: string;
	score: number;
	/** Present on item hits only (architecture plan §5/§7) — avoids a second lookup. */
	storage_id?: number;
	/** Present on both item and storage hits — two root storages can share a
	 * name across different Locations (§3), so a storage hit needs this too
	 * to stay distinguishable in a suggestion list. */
	breadcrumb?: string;
}

/** An item as returned by /api/scan and /api/resolve-storage: the item plus
 * its storage's breadcrumb text (Location prepended when assigned), so the
 * ambiguous-code picker (§6) can tell items sharing a code apart. Not a
 * schema column — kept off Item itself. */
export type ScanItem = Item & { breadcrumb: string };

export type ScanResult =
	| { kind: 'item'; items: ScanItem[] }
	| { kind: 'storage'; storage: Storage }
	| { kind: 'none' };

export type ResolveStorageResult =
	| { kind: 'storage'; storage_id: number }
	| { kind: 'items'; items: ScanItem[] } // ambiguous: caller shows the short picker (§6/§7)
	| { kind: 'none' };

/** An item leaf under a StorageTreeNode. */
export interface StorageTreeItem {
	id: number;
	name: string;
	quantity: number;
}

/** One node of GET /api/storages/tree (the dashboard tree diagram). Without a
 * ?depth= every level is returned, so children.length === child_count; with
 * one, children.length < child_count means the branch was cut. `items` are
 * the items stored directly in this storage. location_name is only ever set
 * on a root storage. */
export interface StorageTreeNode {
	id: number;
	name: string;
	location_name: string | null;
	child_count: number;
	children: StorageTreeNode[];
	items: StorageTreeItem[];
}

/** GET /api/version — the footer label. commit is "" for an unstamped local build. */
export interface VersionInfo {
	version: string;
	commit: string;
}

export class ApiError extends Error {
	status: number;
	constructor(status: number, message: string) {
		super(message);
		this.status = status;
	}
}
