// Minimal service worker (architecture plan §6) — HoardQR is a networked
// homelab tool by design, no offline-first support needed. This exists only
// so the app satisfies home-screen installability. Deliberately no `fetch`
// handler: Chrome hasn't required one for installability since ~2021, and a
// passthrough handler would sit in front of Phase 3's SPA-fallback rewrite
// (see the +layout.ts note) for no benefit — only a place for that to break.

self.addEventListener('install', () => {
	self.skipWaiting();
});

self.addEventListener('activate', (event) => {
	event.waitUntil(self.clients.claim());
});
