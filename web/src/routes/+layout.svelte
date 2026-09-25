<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import './layout.css';
	import SearchBar from '$lib/components/search-bar.svelte';
	import { Button } from '$lib/components/ui/button';
	import ScanLine from '@lucide/svelte/icons/scan-line';
	import House from '@lucide/svelte/icons/house';
	import Box from '@lucide/svelte/icons/box';
	import Wrench from '@lucide/svelte/icons/wrench';

	let { children } = $props();

	onMount(() => {
		if ('serviceWorker' in navigator) {
			// Registration rejects outside a secure context (plain http:// on a LAN
			// IP, e.g.) — expected there, not an error worth surfacing to the user.
			navigator.serviceWorker.register('/service-worker.js').catch(() => {});
		}
	});

	// Bottom tab bar's own primary nav — the header never grew one (see the
	// tabs array below). Storage/Objects stay "active" while browsing into a
	// single storage/item too (startsWith, not exact match): those pages are
	// reached from and conceptually part of that section, not the dashboard.
	const tabs = [
		{ href: '/', label: 'Home', icon: House, active: () => page.url.pathname === '/' },
		{
			href: '/storages',
			label: 'Storage',
			icon: Box,
			// /locations (manage Locations) is reached from and conceptually
			// part of the Storage section too — same startsWith-not-exact-match
			// rationale as Storage/Objects already using it for /storages/42
			// and /items/new.
			active: () => page.url.pathname.startsWith('/storages') || page.url.pathname.startsWith('/locations')
		},
		{ href: '/items', label: 'Items', icon: Wrench, active: () => page.url.pathname.startsWith('/items') }
	];
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
	<main class="mx-auto w-full max-w-lg flex-1 p-4 pb-20 print:max-w-none print:p-0">
		{@render children()}
	</main>
	<nav
		class="glass-panel glass-panel-shadow-up sticky bottom-0 z-10 flex rounded-none border-x-0 border-b-0 print:hidden"
		style="padding-bottom: env(safe-area-inset-bottom)"
	>
		{#each tabs as t (t.href)}
			{@const isActive = t.active()}
			<a
				href={t.href}
				aria-current={isActive ? 'page' : undefined}
				class="flex flex-1 flex-col items-center gap-0.5 py-2 text-xs {isActive
					? 'text-primary font-semibold'
					: 'text-muted-foreground font-normal'}"
			>
				<t.icon size={22} strokeWidth={isActive ? 2.5 : 2} />
				{t.label}
			</a>
		{/each}
	</nav>
</div>
