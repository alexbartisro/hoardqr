<script lang="ts">
	// Direct children + direct items only (drill-down browsing), not the recursive
	// query from §3 — that one backs the scan flow's "contents view" instead
	// (getLocationContents in $lib/api.ts), where scanning a box should surface
	// everything nested inside it in one shot rather than one level at a time.
	import { page } from '$app/state';
	import { getItems, getLocation, getLocations } from '$lib/api';
	import type { Breadcrumb, Item, Location } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';

	let location = $state<Location | null>(null);
	let breadcrumb = $state<Breadcrumb>([]);
	let children = $state<Location[]>([]);
	let items = $state<Item[]>([]);
	let loadError = $state<string | null>(null);

	// See items/[id]'s loadSeq comment — same stale-response guard.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		location = null;
		loadError = null;
		Promise.all([getLocation(id), getLocations(id), getItems({ location_id: id })])
			.then(([loc, childLocations, locItems]) => {
				if (seq !== loadSeq) return;
				location = loc.location;
				breadcrumb = loc.breadcrumb;
				children = childLocations;
				items = locItems;
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load location.';
			});
	});
</script>

<div class="flex flex-col gap-4">
	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !location}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		<div class="flex flex-wrap items-center justify-between gap-2">
			<div class="flex flex-wrap items-center gap-1 text-sm">
				{#each breadcrumb.slice(0, -1) as b (b.id)}
					<a href="/locations/{b.id}" class="hover:underline">{b.name}</a>
					<span class="text-muted-foreground">›</span>
				{/each}
				<span class="font-medium">{location.name}</span>
			</div>
			<Button variant="outline" size="sm" href="/locations/{location.id}/label">Print label</Button>
		</div>

		{#if children.length > 0}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-sm font-medium">Storage</h2>
				{#each children as c (c.id)}
					<a href="/locations/{c.id}" class="block">
						<Card.Root variant="glass" class="p-3">
							<Card.Content class="p-0 text-sm">{c.name}</Card.Content>
						</Card.Root>
					</a>
				{/each}
			</div>
		{/if}

		{#if items.length > 0}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-sm font-medium">Items</h2>
				{#each items as it (it.id)}
					<a href="/items/{it.id}" class="block">
						<Card.Root variant="glass" class="p-3">
							<Card.Content class="p-0 text-sm">{it.name}</Card.Content>
						</Card.Root>
					</a>
				{/each}
			</div>
		{/if}

		{#if children.length === 0 && items.length === 0}
			<p class="text-muted-foreground text-sm">Nothing here yet.</p>
		{/if}
	{/if}
</div>
