// Field names match the Postgres schema (architecture plan §3) exactly, snake_case
// included, since that's what the real JSON API will return — no translation layer
// to maintain when Phase 3 swaps lib/api.ts's mock bodies for real fetch() calls.

export interface Location {
	id: number;
	parent_id: number | null;
	owner_id: number | null;
	is_shared: boolean;
	name: string;
	qr_token: string;
	photo_url: string | null;
	notes: string | null;
	created_at: string;
}

export interface Item {
	id: number;
	location_id: number;
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
	receipt_url: string | null;
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
export type Breadcrumb = Pick<Location, 'id' | 'name'>[];

export interface SearchSuggestion {
	kind: 'item' | 'location' | 'tag';
	id: number;
	name: string;
	score: number;
	/** Present on item hits only (architecture plan §5/§7) — avoids a second lookup. */
	location_id?: number;
	breadcrumb?: string;
}

export type ScanResult =
	| { kind: 'item'; items: Item[] }
	| { kind: 'location'; location: Location }
	| { kind: 'none' };

export type ResolveLocationResult =
	| { kind: 'location'; location_id: number }
	| { kind: 'items'; items: Item[] } // ambiguous: caller shows the short picker (§6/§7)
	| { kind: 'none' };

export class ApiError extends Error {
	status: number;
	constructor(status: number, message: string) {
		super(message);
		this.status = status;
	}
}
