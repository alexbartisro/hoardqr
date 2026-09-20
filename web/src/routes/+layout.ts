// SPA mode: adapter-static builds this to plain files served by the Go binary
// (go:embed) hitting a REST API at runtime — no server-side rendering, no
// prerendering of routes with dynamic params (item/storage detail pages).
export const ssr = false;
export const prerender = false;
