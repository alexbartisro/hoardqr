<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import Box from '@lucide/svelte/icons/box';
	import Wrench from '@lucide/svelte/icons/wrench';
	import { getStorageTree } from '$lib/api';
	import type { StorageTreeItem, StorageTreeNode } from '$lib/types';

	// Dashboard tree diagram: the whole storage hierarchy, every level down to
	// the bottom, with each storage's items as leaves. Drawn as a vertical
	// indented tree (a phone is ~375px wide — a left-to-right layout runs out
	// of room by level 3). One glass panel holds it, scrolling internally so a
	// big inventory can't make the dashboard endlessly tall; the nodes inside
	// are plain bordered chips, not one backdrop-blurred Card each, since
	// hundreds of stacked blur layers are costly on phones. Every storage and
	// item links to its own page.

	let roots = $state<StorageTreeNode[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	$effect(() => {
		getStorageTree()
			.then((tree) => {
				roots = tree;
			})
			.catch((e) => {
				error = e instanceof Error ? e.message : 'Failed to load the storage map.';
			})
			.finally(() => {
				loading = false;
			});
	});
</script>

{#snippet chip(node: StorageTreeNode)}
	<a
		href="/storages/{node.id}"
		class="bg-card/60 hover:bg-accent flex min-w-0 max-w-full items-center gap-2 rounded-md border px-2 py-1.5 text-sm"
	>
		<Box class="text-muted-foreground size-4 shrink-0" />
		<span class="min-w-0">
			<span class="block truncate">{node.name}</span>
			{#if node.location_name}
				<span class="text-muted-foreground block truncate text-xs">{node.location_name}</span>
			{/if}
		</span>
	</a>
{/snippet}

{#snippet itemRow(item: StorageTreeItem)}
	<a
		href="/items/{item.id}"
		class="text-muted-foreground hover:bg-accent hover:text-foreground flex min-w-0 max-w-full items-center gap-2 rounded-md px-2 py-1 text-sm"
	>
		<Wrench class="size-3.5 shrink-0" />
		<span class="min-w-0 truncate">{item.name}</span>
		{#if item.quantity > 1}<span class="shrink-0 text-xs">×{item.quantity}</span>{/if}
	</a>
{/snippet}

<!-- Connector lines are per-<li> pseudo-elements: ::before is the vertical
     spine (full height, but only down to the tick for the last sibling) and
     ::after the horizontal tick into the row. Sibling spacing is padding on
     the <li> (pt-1.5), not a flex gap, so each spine covers the whole gap
     and the line is continuous. A node's child storages and its items share
     ONE list (storages first), so the spine's `last:` handling works across
     both. Tick offsets: 22px = 6px padding + half a single-line chip
     (16px); 20px = 6px + half an item row (14px). Roots (`nested` false)
     get no lines. -->
{#snippet branch(node: StorageTreeNode | null, nodes: StorageTreeNode[], nested: boolean)}
	{@const chipLine =
		'pl-4 pt-1.5 before:absolute before:top-0 before:left-0 before:h-full before:border-l before:border-border last:before:h-[22px] after:absolute after:top-[22px] after:left-0 after:w-4 after:border-t after:border-border'}
	{@const itemLine =
		'pl-4 pt-1.5 before:absolute before:top-0 before:left-0 before:h-full before:border-l before:border-border last:before:h-[20px] after:absolute after:top-[20px] after:left-0 after:w-4 after:border-t after:border-border'}
	<ul class="flex flex-col {nested ? 'ml-3' : 'gap-1.5'}">
		{#each nodes as child (child.id)}
			<li class="relative min-w-0 {nested ? chipLine : ''}">
				{@render chip(child)}
				{#if child.children.length > 0 || child.items.length > 0}
					{@render branch(child, child.children, true)}
				{/if}
			</li>
		{/each}
		{#if node}
			{#each node.items as item (item.id)}
				<li class="relative min-w-0 {itemLine}">
					{@render itemRow(item)}
				</li>
			{/each}
		{/if}
	</ul>
{/snippet}

<!-- The heading lives here (not in the dashboard) so an empty install shows
     no orphaned "Storage map" title over nothing. -->
{#if loading || error || roots.length > 0}
	<div class="mt-2 flex flex-col gap-2">
		<h2 class="text-muted-foreground text-sm font-medium">Storage map</h2>
		{#if loading}
			<p class="text-muted-foreground text-sm">Loading…</p>
		{:else if error}
			<p class="text-destructive text-sm">{error}</p>
		{:else}
			<Card.Root variant="glass" class="p-3">
				<Card.Content class="p-0">
					<nav aria-label="Storage map" class="max-h-[60vh] overflow-y-auto overscroll-contain md:max-h-[75vh]">
						{@render branch(null, roots, false)}
					</nav>
				</Card.Content>
			</Card.Root>
		{/if}
	</div>
{/if}
