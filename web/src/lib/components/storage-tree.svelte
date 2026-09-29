<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import Box from '@lucide/svelte/icons/box';
	import { getStorageTree } from '$lib/api';
	import type { StorageTreeNode } from '$lib/types';

	// Dashboard tree diagram: the first few levels of the storage hierarchy,
	// drawn as a vertical indented tree (a phone is ~375px wide — a
	// left-to-right layout runs out of room by level 3). One glass panel holds
	// the whole thing; the nodes inside are plain bordered chips, not one
	// backdrop-blurred Card each, since dozens of stacked blur layers are
	// costly on phones. Every node links to its storage page.

	// A node with more children than this shows the first few plus a
	// "+N more" link to its own page, so one crowded shelf can't make the
	// dashboard endlessly tall.
	const MAX_CHILDREN_SHOWN = 8;

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

<!-- Connector lines are per-<li> pseudo-elements: ::before is the vertical
     spine (full height, but only down to the tick for the last sibling) and
     ::after the horizontal tick into the chip. `nested` is false for the
     roots, which get no lines. `parent` is the node these are the children
     of (undefined for the roots): when it has children not shown — cut by
     the API's depth cap or by MAX_CHILDREN_SHOWN — a final "+N more" <li>
     joins the same list, so the last real sibling's spine continues into it
     and the "+N more" row is the one that ends the line. -->
{#snippet branch(nodes: StorageTreeNode[], nested: boolean, parent?: StorageTreeNode)}
	{@const lineClass =
		'pl-4 before:absolute before:top-0 before:left-0 before:h-full before:border-l before:border-border last:before:h-4 after:absolute after:top-4 after:left-0 after:w-4 after:border-t after:border-border'}
	<ul class="flex flex-col gap-1.5 {nested ? 'mt-1.5 ml-3' : ''}">
		{#each nodes.slice(0, MAX_CHILDREN_SHOWN) as node (node.id)}
			<li class="relative min-w-0 {nested ? lineClass : ''}">
				{@render chip(node)}
				{#if node.children.length > 0 || node.child_count > 0}
					{@render branch(node.children, true, node)}
				{/if}
			</li>
		{/each}
		{#if parent}
			{@const hidden = parent.child_count - Math.min(parent.children.length, MAX_CHILDREN_SHOWN)}
			{#if hidden > 0}
				<li class="relative min-w-0 {lineClass}">
					<a href="/storages/{parent.id}" class="text-primary text-xs hover:underline">+{hidden} more →</a>
				</li>
			{/if}
		{/if}
	</ul>
{/snippet}

{#if loading}
	<p class="text-muted-foreground text-sm">Loading…</p>
{:else if error}
	<p class="text-destructive text-sm">{error}</p>
{:else if roots.length > 0}
	<Card.Root variant="glass" class="p-3">
		<Card.Content class="p-0">
			<nav aria-label="Storage map">
				{@render branch(roots, false)}
			</nav>
		</Card.Content>
	</Card.Root>
{/if}
