// Item.condition (types.ts) is plain `string | null` — no DB-level enum
// (architecture plan §3's `condition TEXT` column has none either) — so
// this is a frontend-only convenience list, not a constraint the backend
// will enforce. Free text was tried first and swapped for this per
// feedback 2026-09-17: a handful of consistent values beats everyone
// typing their own wording for the same thing. Shared between items/new
// (create) and the item detail page's Details editor (edit) — added
// 2026-09-20 when the same list was about to be needed a second place.
export const CONDITIONS = ['new', 'good', 'fair', 'poor'];
