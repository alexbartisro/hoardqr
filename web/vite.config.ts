import tailwindcss from '@tailwindcss/vite';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	server: {
		// Phase 3 step 4: lib/api.ts now calls fetch() with relative paths,
		// which resolve against Vite's own dev-server origin (5173) unless
		// proxied — in production the SvelteKit build is embedded into the same
		// Go binary that serves the API (same origin, no proxy needed there).
		// Points at the Go server's default `serve` port; override with
		// VITE_API_PROXY_TARGET if it's running elsewhere.
		proxy: {
			'/api': process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8080',
			'/healthz': process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8080',
			// Uploaded photos (§12) are served from /uploads/, not /api/ — same
			// reasoning as the two routes above: without this, an <img src>
			// pointing at a photo_url resolves against Vite's own origin (5173)
			// instead of the Go server that actually has the file.
			'/uploads': process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8080'
		}
	},
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) => filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter({
				pages: 'build',
				assets: 'build',
				fallback: 'index.html',
				precompress: false,
				strict: true
			})
		})
	]
});
