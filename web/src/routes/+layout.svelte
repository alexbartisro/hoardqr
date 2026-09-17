<script lang="ts">
	import { onMount } from 'svelte';
	import './layout.css';
	import SearchBar from '$lib/components/search-bar.svelte';
	import { Button } from '$lib/components/ui/button';
	import ScanLine from '@lucide/svelte/icons/scan-line';

	let { children } = $props();

	onMount(() => {
		if ('serviceWorker' in navigator) {
			// Registration rejects outside a secure context (plain http:// on a LAN
			// IP, e.g.) — expected there, not an error worth surfacing to the user.
			navigator.serviceWorker.register('/service-worker.js').catch(() => {});
		}
	});
</script>

<svelte:head><link rel="icon" href="/icons/icon-192.png" /></svelte:head>

<div class="flex min-h-screen flex-col">
	<header
		class="glass-panel sticky top-0 z-10 flex items-center gap-3 rounded-none border-x-0 border-t-0 px-4 py-3 print:hidden"
	>
		<a href="/" class="shrink-0 text-lg font-semibold">HoardQR</a>
		<SearchBar />
		<Button href="/scan" variant="ghost" size="icon" class="shrink-0" aria-label="Scan a code">
			<ScanLine />
		</Button>
	</header>
	<main class="mx-auto w-full max-w-lg flex-1 p-4 print:max-w-none print:p-0">
		{@render children()}
	</main>
</div>
