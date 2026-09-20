import type { Item, Storage, Tag, User } from './types';

// A handful of realistic storages/items shaped exactly like the §3 schema, per the
// development plan's Phase 2 step 2. api.ts clones these at import time and mutates
// the clone — reload the app to reset back to this seed state.

export const STORAGES: Storage[] = [
	{
		id: 1,
		parent_id: null,
		owner_id: 1,
		is_shared: true,
		name: 'Balcony',
		qr_token: 'H4K9P2',
		photo_url: null,
		notes: null,
		location_id: null,
		created_at: '2026-01-05T09:00:00Z'
	},
	{
		id: 2,
		parent_id: 1,
		owner_id: 1,
		is_shared: true,
		name: 'Storage Cabinet',
		qr_token: '8f2a91c4e7',
		photo_url: null,
		notes: null,
		location_id: null,
		created_at: '2026-01-05T09:05:00Z'
	},
	{
		id: 3,
		parent_id: 2,
		owner_id: 1,
		is_shared: true,
		name: 'Shelf 2',
		qr_token: '3d7b5e19a2',
		photo_url: null,
		notes: null,
		location_id: null,
		created_at: '2026-01-05T09:06:00Z'
	},
	{
		id: 4,
		parent_id: 3,
		owner_id: 1,
		is_shared: true,
		name: 'Box 4',
		qr_token: 'b91f4c0a6d',
		photo_url: null,
		notes: 'Electronics odds and ends',
		location_id: null,
		created_at: '2026-01-05T09:07:00Z'
	},
	{
		id: 5,
		parent_id: null,
		owner_id: 1,
		is_shared: true,
		name: 'Garage',
		qr_token: 'R5T8W3',
		photo_url: null,
		notes: null,
		location_id: null,
		created_at: '2026-01-06T10:00:00Z'
	},
	{
		id: 6,
		parent_id: 5,
		owner_id: 1,
		is_shared: true,
		name: 'Tool Chest',
		qr_token: '0e4a7f2c19',
		photo_url: null,
		notes: null,
		location_id: null,
		created_at: '2026-01-06T10:10:00Z'
	},
	{
		id: 7,
		parent_id: null,
		owner_id: 1,
		is_shared: true,
		name: 'Living Room',
		qr_token: '9c3e8b41f0',
		photo_url: null,
		notes: null,
		location_id: null,
		created_at: '2026-01-07T11:00:00Z'
	},
	{
		id: 8,
		parent_id: 7,
		owner_id: 1,
		is_shared: true,
		name: 'TV Console',
		qr_token: '5a1d9e7c3b',
		photo_url: null,
		notes: null,
		location_id: null,
		created_at: '2026-01-07T11:05:00Z'
	}
];

export const ITEMS: Item[] = [
	{
		id: 101,
		storage_id: 4,
		owner_id: 1,
		is_shared: true,
		name: 'Multimeter',
		description: 'Fluke 115',
		quantity: 1,
		condition: 'good',
		qr_token: 'e2f8a417c9',
		photo_url: null,
		purchase_date: '2024-03-12',
		purchase_price: 129.99,
		receipt_url: null,
		custom_fields: {},
		created_at: '2026-01-05T09:10:00Z',
		updated_at: '2026-01-05T09:10:00Z',
		tags: ['electronics', 'tools']
	},
	{
		id: 102,
		storage_id: 6,
		owner_id: 1,
		is_shared: true,
		name: 'Cordless Drill',
		description: null,
		quantity: 1,
		condition: 'good',
		qr_token: '040901234567', // adopted retail barcode (§4)
		photo_url: null,
		purchase_date: '2023-11-02',
		purchase_price: 89.0,
		receipt_url: null,
		custom_fields: {},
		created_at: '2026-01-06T10:15:00Z',
		updated_at: '2026-01-06T10:15:00Z',
		tags: ['tools']
	},
	{
		id: 103,
		storage_id: 8,
		owner_id: 1,
		is_shared: true,
		name: 'HDMI Cable',
		description: '2m braided',
		quantity: 1,
		condition: 'good',
		qr_token: '8801643216405', // same retail barcode as item 104, different location (§3–4)
		photo_url: null,
		purchase_date: null,
		purchase_price: null,
		receipt_url: null,
		custom_fields: {},
		created_at: '2026-01-07T11:10:00Z',
		updated_at: '2026-01-07T11:10:00Z',
		tags: ['electronics', 'cables']
	},
	{
		id: 104,
		storage_id: 2,
		owner_id: 1,
		is_shared: true,
		name: 'HDMI Cable',
		description: '2m braided, spare',
		quantity: 1,
		condition: 'good',
		qr_token: '8801643216405',
		photo_url: null,
		purchase_date: null,
		purchase_price: null,
		receipt_url: null,
		custom_fields: {},
		created_at: '2026-01-08T09:00:00Z',
		updated_at: '2026-01-08T09:00:00Z',
		tags: ['electronics', 'cables']
	},
	{
		id: 105,
		storage_id: 2,
		owner_id: 1,
		is_shared: true,
		name: 'Christmas Lights',
		description: null,
		quantity: 3,
		condition: 'good',
		qr_token: 'N3F7K1', // generated plain-text code (§4)
		photo_url: null,
		purchase_date: null,
		purchase_price: null,
		receipt_url: null,
		custom_fields: {},
		created_at: '2026-01-08T09:05:00Z',
		updated_at: '2026-01-08T09:05:00Z',
		tags: ['seasonal']
	},
	{
		id: 106,
		storage_id: 7,
		owner_id: 1,
		is_shared: false, // per-object sharing (§10) — stays private even though the room isn't
		name: 'Passport',
		description: null,
		quantity: 1,
		condition: null,
		qr_token: '1a6d9f3b52',
		photo_url: null,
		purchase_date: null,
		purchase_price: null,
		receipt_url: null,
		custom_fields: {},
		created_at: '2026-01-09T08:00:00Z',
		updated_at: '2026-01-09T08:00:00Z',
		tags: []
	}
];

export const TAGS: Tag[] = [
	{ id: 1, name: 'electronics' },
	{ id: 2, name: 'tools' },
	{ id: 3, name: 'seasonal' },
	{ id: 4, name: 'cables' }
];

export const USERS: User[] = [
	{ id: 1, username: 'alex', role: 'admin', created_at: '2026-01-05T08:55:00Z' }
];
