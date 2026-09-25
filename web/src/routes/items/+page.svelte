<script lang="ts">
	import { page as pageState } from '$app/state';
	import { getRecentItems } from '$lib/api';
	import type { Item } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import Wrench from '@lucide/svelte/icons/wrench';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import { Button } from '$lib/components/ui/button';

	// Optional ?tag= filter (added 2026-09-21) — the header search bar's tag
	// suggestions navigate here so they're actually clickable, instead of
	// rendering as a dead-end chip (see search-bar.svelte and CLAUDE.md).
	// $derived, not captured once: this page can be revisited with a
	// different ?tag= without remounting (same component-reuse concern
	// documented elsewhere for [id] routes).
	let tag = $derived(pageState.url.searchParams.get('tag'));

	// Built on getRecentItems, not getItems: it returns {item, breadcrumb}
	// pairs (getItems doesn't include a breadcrumb at all), and it's
	// paginated server-side (getItems isn't, and returns literally everything
	// matching with no total count) — both matter for a page whose whole job
	// is "browse everything I own". Still comfortably under the server's
	// pageSize cap of 100 (internal/api/items.go's maxRecentItemsPageSize).
	const PAGE_SIZE = 20;

	let page = $state(1);

	// Changing the tag filter (a fresh navigation to this same route with a
	// different ?tag=) resets back to page 1 — otherwise landing here while
	// already on page 3 would ask for page 3 of a different, likely much
	// smaller, filtered result set.
	$effect(() => {
		tag;
		page = 1;
	});
	let entries = $state<{ item: Item; breadcrumb: string }[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let loadError = $state<string | null>(null);

	// See CLAUDE.md's note on this pattern. Keyed on both page and tag —
	// changing the tag filter resets back to page 1 (below) but that's a
	// separate write from this effect's own re-run, so both need to be
	// tracked as dependencies here.
	let loadSeq = 0;
	$effect(() => {
		const p = page;
		const t = tag;
		const seq = ++loadSeq;
		loading = true;
		loadError = null;
		getRecentItems({ page: p, pageSize: PAGE_SIZE, tag: t ?? undefined })
			.then((res) => {
				if (seq !== loadSeq) return;
				entries = res.entries;
				total = res.total;
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load items.';
			})
			.finally(() => {
				if (seq !== loadSeq) return;
				loading = false;
			});
	});

	let totalPages = $derived(Math.max(1, Math.ceil(total / PAGE_SIZE)));

	function formatDate(iso: string): string {
		const d = new Date(iso);
		const dd = String(d.getUTCDate()).padStart(2, '0');
		const mm = String(d.getUTCMonth() + 1).padStart(2, '0');
		return `${dd}.${mm}.${d.getUTCFullYear()}`;
	}
</script>

<div class="flex flex-col gap-4">
	<div class="flex items-center justify-between gap-2">
		<h1 class="text-xl font-semibold">Objects</h1>
		<Button size="sm" href="/items/new">+ Add Object</Button>
	</div>

	{#if tag}
		<div class="flex items-center gap-2 text-sm">
			<span class="text-muted-foreground">Tagged</span>
			<span class="bg-secondary text-secondary-foreground rounded-full px-3 py-1 text-xs">{tag}</span>
			<a href="/items" class="text-primary hover:underline">Clear</a>
		</div>
	{/if}

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if entries.length === 0}
		<p class="text-muted-foreground text-sm">
			{#if tag}
				No items tagged "{tag}".
			{:else}
				No items yet. <a href="/items/new" class="text-primary hover:underline">Add one →</a>
			{/if}
		</p>
	{:else}
		<div class="flex flex-col gap-2">
			{#each entries as { item, breadcrumb } (item.id)}
				<a href="/items/{item.id}" class="block">
					<Card.Root variant="glass" class="p-3">
						<Card.Content class="flex flex-row items-center gap-2 p-0 text-sm">
							<Wrench class="text-muted-foreground size-4 shrink-0" />
							<div class="flex min-w-0 flex-1 flex-col">
								<span class="truncate font-medium">{item.name}</span>
								<span class="text-muted-foreground truncate text-xs">{breadcrumb}</span>
							</div>
							<span class="text-muted-foreground shrink-0 text-xs">{formatDate(item.created_at)}</span>
							<ChevronRight class="text-muted-foreground/60 size-4 shrink-0" />
						</Card.Content>
					</Card.Root>
				</a>
			{/each}
		</div>

		{#if totalPages > 1}
			<div class="flex items-center justify-between pt-1">
				<Button variant="outline" size="sm" disabled={page <= 1} onclick={() => (page -= 1)}>
					Previous
				</Button>
				<span class="text-muted-foreground text-xs">Page {page} of {totalPages}</span>
				<Button variant="outline" size="sm" disabled={page >= totalPages} onclick={() => (page += 1)}>
					Next
				</Button>
			</div>
		{/if}
	{/if}
</div>
