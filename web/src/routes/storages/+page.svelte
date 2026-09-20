<script lang="ts">
	import { getStorages } from '$lib/api';
	import type { Storage } from '$lib/types';
	import StorageTreeNode from '$lib/components/storage-tree-node.svelte';

	let roots = $state<Storage[]>([]);
	let loading = $state(true);

	$effect(() => {
		getStorages().then((storages) => {
			roots = storages;
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
			No storage yet. <a href="/storages/new" class="text-primary hover:underline">Add one →</a>
		</p>
	{:else}
		<div class="flex flex-col gap-2">
			{#each roots as storage (storage.id)}
				<StorageTreeNode {storage} />
			{/each}
		</div>
	{/if}
</div>
