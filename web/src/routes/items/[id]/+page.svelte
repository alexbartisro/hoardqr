<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { deleteItem, getItemById } from '$lib/api';
	import type { Breadcrumb, Item } from '$lib/types';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';

	let item = $state<Item | null>(null);
	let breadcrumb = $state<Breadcrumb>([]);
	let loadError = $state<string | null>(null);
	let deleting = $state(false);

	// Guards against a slower response for a since-abandoned id winning over a
	// faster one for the current id — same pattern as location-picker.svelte's
	// search debounce, needed anywhere an $effect kicks off a fetch keyed by a
	// value that can change again before that fetch resolves.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		item = null;
		loadError = null;
		getItemById(id)
			.then((r) => {
				if (seq !== loadSeq) return;
				item = r.item;
				breadcrumb = r.breadcrumb;
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load item.';
			});
	});

	async function handleDelete() {
		if (!item || !confirm(`Delete "${item.name}"?`)) return;
		deleting = true;
		try {
			await deleteItem(item.id);
			await goto(`/locations/${item.location_id}`);
		} catch (e) {
			loadError = e instanceof Error ? e.message : 'Failed to delete item.';
			deleting = false;
		}
	}
</script>

<div class="flex flex-col gap-4">
	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !item}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		<div class="flex flex-wrap items-center gap-1 text-sm">
			{#each breadcrumb as b (b.id)}
				<a href="/locations/{b.id}" class="hover:underline">{b.name}</a>
				<span class="text-muted-foreground">›</span>
			{/each}
			<span class="font-medium">{item.name}</span>
		</div>

		<Card.Root variant="glass" class="p-4">
			<Card.Header class="p-0">
				<Card.Title class="text-xl">{item.name}</Card.Title>
				{#if item.description}<Card.Description>{item.description}</Card.Description>{/if}
			</Card.Header>
			<Card.Content class="mt-4 flex flex-col gap-2 p-0 text-sm">
				<div class="flex justify-between">
					<span class="text-muted-foreground">Quantity</span><span>{item.quantity}</span>
				</div>
				{#if item.condition}
					<div class="flex justify-between">
						<span class="text-muted-foreground">Condition</span><span>{item.condition}</span>
					</div>
				{/if}
				<div class="flex justify-between">
					<span class="text-muted-foreground">Code</span><span class="font-mono">{item.qr_token}</span>
				</div>
				{#if item.purchase_date}
					<div class="flex justify-between">
						<span class="text-muted-foreground">Purchased</span><span>{item.purchase_date}</span>
					</div>
				{/if}
				{#if item.purchase_price != null}
					<div class="flex justify-between">
						<span class="text-muted-foreground">Price</span><span>{item.purchase_price}</span>
					</div>
				{/if}
				{#if !item.is_shared}
					<p class="text-muted-foreground">Private — not shared</p>
				{/if}
			</Card.Content>
			{#if item.tags.length > 0}
				<div class="mt-4 flex flex-wrap gap-2">
					{#each item.tags as t (t)}
						<span class="bg-secondary text-secondary-foreground rounded-full px-3 py-1 text-xs">{t}</span>
					{/each}
				</div>
			{/if}
		</Card.Root>

		<Button variant="destructive" onclick={handleDelete} disabled={deleting}>
			{deleting ? 'Deleting…' : 'Delete'}
		</Button>
	{/if}
</div>
