<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import Package from '@lucide/svelte/icons/package';
	import FolderPlus from '@lucide/svelte/icons/folder-plus';
	import Folder from '@lucide/svelte/icons/folder';
	import { getRecentItems, getStorages } from '$lib/api';
	import type { Item, Storage } from '$lib/types';

	// Both previews are teasers, not full browsers — "See all" hands off to
	// /storages and /items (the tab bar's own destinations), which is where
	// the dashboard's old paginated Recent feed moved to. Capped small on
	// purpose: this is a glance, not the primary way to browse either list
	// anymore.
	const RECENT_PREVIEW_SIZE = 5;
	const STORAGE_PREVIEW_SIZE = 6;

	let recentEntries = $state<{ item: Item; breadcrumb: string }[]>([]);
	let recentTotal = $state(0);
	let recentLoading = $state(true);

	let roots = $state<Storage[]>([]);
	let rootsLoading = $state(true);

	$effect(() => {
		getRecentItems({ page: 1, pageSize: RECENT_PREVIEW_SIZE }).then((res) => {
			recentEntries = res.entries;
			recentTotal = res.total;
			recentLoading = false;
		});
		// Root storages for a home inventory are structurally few (rooms/areas,
		// not hundreds) — there's no paginated storages endpoint, so fetching
		// all of them and slicing client-side is the right call here, not a
		// shortcut around missing pagination.
		getStorages().then((storages) => {
			roots = storages;
			rootsLoading = false;
		});
	});

	function formatDate(iso: string): string {
		const d = new Date(iso);
		const dd = String(d.getUTCDate()).padStart(2, '0');
		const mm = String(d.getUTCMonth() + 1).padStart(2, '0');
		return `${dd}.${mm}.${d.getUTCFullYear()}`;
	}
</script>

<div class="flex flex-col gap-4">
	<div class="grid grid-cols-2 gap-3">
		<a href="/items/new" class="block">
			<Card.Root variant="glass" class="transition-transform active:scale-[0.98]">
				<Card.Content class="flex flex-col items-center gap-2 py-2 text-center">
					<Package class="text-primary size-7" />
					<Card.Title class="text-sm">Add Object</Card.Title>
				</Card.Content>
			</Card.Root>
		</a>

		<a href="/storages/new" class="block">
			<Card.Root variant="glass" class="transition-transform active:scale-[0.98]">
				<Card.Content class="flex flex-col items-center gap-2 py-2 text-center">
					<FolderPlus class="text-primary size-7" />
					<Card.Title class="text-sm">Add Storage</Card.Title>
				</Card.Content>
			</Card.Root>
		</a>
	</div>

	<div class="mt-2 flex flex-col gap-2">
		<div class="flex items-center justify-between">
			<h2 class="text-muted-foreground text-sm font-medium">Storage</h2>
			{#if roots.length > STORAGE_PREVIEW_SIZE}
				<a href="/storages" class="text-primary text-xs hover:underline">See all →</a>
			{/if}
		</div>

		{#if rootsLoading}
			<p class="text-muted-foreground text-sm">Loading…</p>
		{:else if roots.length === 0}
			<p class="text-muted-foreground text-sm">No storage yet.</p>
		{:else}
			{#each roots.slice(0, STORAGE_PREVIEW_SIZE) as storage (storage.id)}
				<a href="/storages/{storage.id}" class="block">
					<Card.Root variant="glass" class="p-3">
						<Card.Content class="flex flex-row items-center gap-2 p-0 text-sm">
							<Folder class="text-muted-foreground size-4 shrink-0" />
							<span class="truncate">{storage.name}</span>
						</Card.Content>
					</Card.Root>
				</a>
			{/each}
		{/if}
	</div>

	<div class="mt-2 flex flex-col gap-2">
		<div class="flex items-center justify-between">
			<h2 class="text-muted-foreground text-sm font-medium">Recent</h2>
			{#if recentTotal > RECENT_PREVIEW_SIZE}
				<a href="/items" class="text-primary text-xs hover:underline">See all →</a>
			{/if}
		</div>

		{#if recentLoading}
			<p class="text-muted-foreground text-sm">Loading…</p>
		{:else if recentEntries.length === 0}
			<p class="text-muted-foreground text-sm">No items yet.</p>
		{:else}
			{#each recentEntries as { item, breadcrumb } (item.id)}
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
		{/if}
	</div>
</div>
