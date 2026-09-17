<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import Package from '@lucide/svelte/icons/package';
	import FolderPlus from '@lucide/svelte/icons/folder-plus';
	import { getRecentItems } from '$lib/api';
	import type { Item } from '$lib/types';

	const PAGE_SIZE = 10;

	let page = $state(1);
	let entries = $state<{ item: Item; breadcrumb: string }[]>([]);
	let total = $state(0);
	let loading = $state(true);

	// See CLAUDE.md's note on this pattern.
	let loadSeq = 0;
	$effect(() => {
		const p = page;
		const seq = ++loadSeq;
		loading = true;
		getRecentItems({ page: p, pageSize: PAGE_SIZE }).then((res) => {
			if (seq !== loadSeq) return;
			entries = res.entries;
			total = res.total;
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
	<a href="/items/new" class="block">
		<Card.Root variant="glass" class="transition-transform active:scale-[0.98]">
			<Card.Content class="flex flex-row items-center gap-4">
				<Package class="text-primary size-8" />
				<div>
					<Card.Title>Add Object</Card.Title>
					<Card.Description>Catalog something new and place it in a location.</Card.Description>
				</div>
			</Card.Content>
		</Card.Root>
	</a>

	<a href="/locations/new" class="block">
		<Card.Root variant="glass" class="transition-transform active:scale-[0.98]">
			<Card.Content class="flex flex-row items-center gap-4">
				<FolderPlus class="text-primary size-8" />
				<div>
					<Card.Title>Add Storage</Card.Title>
					<Card.Description>Create a new box, shelf, or other storage location.</Card.Description>
				</div>
			</Card.Content>
		</Card.Root>
	</a>

	<div class="mt-2 flex flex-col gap-2">
		<h2 class="text-muted-foreground text-sm font-medium">Recent</h2>

		{#if loading}
			<p class="text-muted-foreground text-sm">Loading…</p>
		{:else if entries.length === 0}
			<p class="text-muted-foreground text-sm">No items yet.</p>
		{:else}
			{#each entries as { item, breadcrumb } (item.id)}
				<a href="/items/{item.id}" class="block">
					<Card.Root variant="glass" class="p-3">
						<Card.Content class="flex flex-row items-center justify-between gap-2 p-0 text-sm">
							<div class="flex min-w-0 flex-col">
								<span class="truncate font-medium">{item.name}</span>
								<span class="text-muted-foreground truncate text-xs">{breadcrumb}</span>
							</div>
							<span class="text-muted-foreground shrink-0 text-xs">{formatDate(item.created_at)}</span>
						</Card.Content>
					</Card.Root>
				</a>
			{/each}

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
</div>
