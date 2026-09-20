<script lang="ts">
	import { getStorages } from '$lib/api';
	import type { Storage } from '$lib/types';
	import StorageTreeNode from '$lib/components/storage-tree-node.svelte';
	import { Button } from '$lib/components/ui/button';

	let roots = $state<Storage[]>([]);
	let loading = $state(true);
	let loadError = $state<string | null>(null);

	$effect(() => {
		getStorages()
			.then((storages) => {
				roots = storages;
			})
			.catch((e) => {
				loadError = e instanceof Error ? e.message : 'Failed to load storages.';
			})
			.finally(() => {
				loading = false;
			});
	});

	// Groups root storages by location_name, "Unassigned" last — this is the
	// actual disambiguation UI for two identically-named storages in
	// different properties (architecture plan §3, the user's own example: two
	// "Living Room"s). A single group renders flat instead (no headings) —
	// nothing to disambiguate yet if every root shares one group.
	let groups = $derived.by(() => {
		const byName = new Map<string, Storage[]>();
		for (const s of roots) {
			const key = s.location_name ?? 'Unassigned';
			if (!byName.has(key)) byName.set(key, []);
			byName.get(key)!.push(s);
		}
		return [...byName.entries()].sort((a, b) => {
			if (a[0] === 'Unassigned') return 1;
			if (b[0] === 'Unassigned') return -1;
			return a[0].localeCompare(b[0]);
		});
	});

	let flat = $derived(groups.length <= 1);
</script>

<div class="flex flex-col gap-4">
	<div class="flex items-center justify-between gap-2">
		<h1 class="text-xl font-semibold">Storage</h1>
		<div class="flex items-center gap-3">
			<a href="/locations" class="text-primary text-sm hover:underline">Manage locations</a>
			<Button size="sm" href="/storages/new">+ Add Storage</Button>
		</div>
	</div>

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if roots.length === 0}
		<p class="text-muted-foreground text-sm">
			No storage yet. <a href="/storages/new" class="text-primary hover:underline">Add one →</a>
		</p>
	{:else if flat}
		<div class="flex flex-col gap-2">
			{#each roots as storage (storage.id)}
				<StorageTreeNode {storage} />
			{/each}
		</div>
	{:else}
		{#each groups as [name, storages] (name)}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-sm font-medium">{name}</h2>
				{#each storages as storage (storage.id)}
					<StorageTreeNode {storage} />
				{/each}
			</div>
		{/each}
	{/if}
</div>
