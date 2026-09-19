<script lang="ts">
	import { getLocations } from '$lib/api';
	import type { Location } from '$lib/types';
	import LocationTreeNode from '$lib/components/location-tree-node.svelte';

	let roots = $state<Location[]>([]);
	let loading = $state(true);

	$effect(() => {
		getLocations().then((locs) => {
			roots = locs;
			loading = false;
		});
	});
</script>

<div class="flex flex-col gap-4">
	<h1 class="text-xl font-semibold">Storage</h1>

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if roots.length === 0}
		<p class="text-muted-foreground text-sm">
			No storage locations yet. <a href="/locations/new" class="text-primary hover:underline">Add one →</a>
		</p>
	{:else}
		<div class="flex flex-col gap-2">
			{#each roots as loc (loc.id)}
				<LocationTreeNode location={loc} />
			{/each}
		</div>
	{/if}
</div>
