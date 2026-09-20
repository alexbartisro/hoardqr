<script lang="ts">
	import { getStorages } from '$lib/api';
	import type { Storage } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import Folder from '@lucide/svelte/icons/folder';
	import FolderOpen from '@lucide/svelte/icons/folder-open';
	// Self-import for recursion — a child node is the same component as its
	// parent, one level down.
	import StorageTreeNode from './storage-tree-node.svelte';

	let { storage }: { storage: Storage } = $props();

	let expanded = $state(false);
	let loaded = $state(false);
	let loading = $state(false);
	let children = $state<Storage[]>([]);

	// Fetched once per node, on first expand, then cached — re-collapsing and
	// re-expanding shouldn't refetch. There's no way to know in advance
	// whether a storage has children (no count comes back from any
	// endpoint), so every row gets a chevron and resolves to "Nothing here
	// yet" on first expand if it turns out to be a leaf.
	async function toggle() {
		if (!expanded && !loaded && !loading) {
			loading = true;
			children = await getStorages(storage.id);
			loaded = true;
			loading = false;
		}
		expanded = !expanded;
	}
</script>

<Card.Root variant="glass" class="p-3">
	<Card.Content class="flex flex-row items-center gap-2 p-0 text-sm">
		<button
			type="button"
			onclick={toggle}
			aria-expanded={expanded}
			aria-label="{expanded ? 'Collapse' : 'Expand'} {storage.name}"
			class="text-muted-foreground flex size-9 shrink-0 items-center justify-center"
		>
			<ChevronRight class="size-4 transition-transform {expanded ? 'rotate-90' : ''}" />
		</button>
		<a href="/storages/{storage.id}" class="flex min-w-0 flex-1 items-center gap-2">
			{#if expanded}
				<FolderOpen class="text-muted-foreground size-4 shrink-0" />
			{:else}
				<Folder class="text-muted-foreground size-4 shrink-0" />
			{/if}
			<span class="truncate">{storage.name}</span>
		</a>
	</Card.Content>
</Card.Root>

{#if expanded}
	<div class="border-border/50 ml-4 flex flex-col gap-2 border-l pl-2">
		{#if loading}
			<p class="text-muted-foreground py-1 text-sm">Loading…</p>
		{:else if children.length === 0}
			<p class="text-muted-foreground py-1 text-sm">Nothing here yet.</p>
		{:else}
			{#each children as child (child.id)}
				<StorageTreeNode storage={child} />
			{/each}
		{/if}
	</div>
{/if}
