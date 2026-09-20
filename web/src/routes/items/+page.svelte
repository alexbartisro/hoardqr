<script lang="ts">
	import { getRecentItems } from '$lib/api';
	import type { Item } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';

	// Built on getRecentItems, not getItems: it returns {item, breadcrumb}
	// pairs (getItems doesn't include a breadcrumb at all), and it's
	// paginated server-side (getItems isn't, and returns literally everything
	// matching with no total count) — both matter for a page whose whole job
	// is "browse everything I own". Still comfortably under the server's
	// pageSize cap of 100 (internal/api/items.go's maxRecentItemsPageSize).
	const PAGE_SIZE = 20;

	let page = $state(1);
	let entries = $state<{ item: Item; breadcrumb: string }[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let loadError = $state<string | null>(null);

	// See CLAUDE.md's note on this pattern.
	let loadSeq = 0;
	$effect(() => {
		const p = page;
		const seq = ++loadSeq;
		loading = true;
		loadError = null;
		getRecentItems({ page: p, pageSize: PAGE_SIZE })
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
	<h1 class="text-xl font-semibold">Objects</h1>

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if entries.length === 0}
		<p class="text-muted-foreground text-sm">
			No items yet. <a href="/items/new" class="text-primary hover:underline">Add one →</a>
		</p>
	{:else}
		<div class="flex flex-col gap-2">
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
