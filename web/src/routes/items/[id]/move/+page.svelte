<script lang="ts">
	// Move an item to a different storage — the web UI's first affordance
	// for this (added 2026-09-20; previously only reachable via MCP's
	// move_item tool). A dedicated sub-route, not an inline control on the
	// item detail page, since it needs StoragePicker's full Type/Scan/OCR
	// surface — too much UI to embed on an already-dense page, and a fresh
	// route gets clean picker state for free instead of adding another
	// live-mutation guard to the detail page's load $effect reset block.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { getItemById, updateItem } from '$lib/api';
	import type { Item } from '$lib/types';
	import StoragePicker from '$lib/components/storage-picker.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';

	let item = $state<Item | null>(null);
	let currentBreadcrumbText = $state('');
	let loadError = $state<string | null>(null);

	let storageId = $state<number | null>(null);
	let breadcrumb = $state('');
	let submitting = $state(false);
	let error = $state<string | null>(null);

	// See items/[id]'s loadSeq comment — same stale-response guard.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		item = null;
		loadError = null;
		error = null;
		getItemById(id)
			.then((r) => {
				if (seq !== loadSeq) return;
				item = r.item;
				currentBreadcrumbText = [
					...(r.location ? [r.location.name] : []),
					...r.breadcrumb.map((b) => b.name)
				].join(' › ');
				// Prefill the picker on the item's current storage so it opens
				// already showing a resolved chip with a "Change" button, not
				// an empty picker — the destination StoragePicker asks for.
				storageId = r.item.storage_id;
				breadcrumb = r.breadcrumb.map((b) => b.name).join(' > ');
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load item.';
			});
	});

	async function submit() {
		if (!item || storageId == null || storageId === item.storage_id) return;
		const id = item.id;
		const seq = loadSeq;
		submitting = true;
		error = null;
		try {
			await updateItem(id, { storage_id: storageId });
			if (seq !== loadSeq) return;
			await goto(`/items/${id}`);
		} catch (e) {
			if (seq !== loadSeq) return;
			error = e instanceof Error ? e.message : 'Failed to move item.';
		} finally {
			if (seq === loadSeq) submitting = false;
		}
	}
</script>

<div class="flex flex-col gap-6">
	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !item}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		<div>
			<h1 class="text-xl font-semibold">Move "{item.name}"</h1>
			<p class="text-muted-foreground text-sm">Currently in {currentBreadcrumbText}</p>
		</div>

		<Card.Root variant="glass" class="p-4">
			<Card.Content class="flex flex-col gap-4 p-0">
				<StoragePicker bind:storageId bind:breadcrumb />

				{#if error}<p class="text-destructive text-sm">{error}</p>{/if}
				<div class="flex gap-2">
					<Button
						onclick={submit}
						disabled={submitting || storageId == null || storageId === item.storage_id}
					>
						{submitting ? 'Moving…' : 'Move here'}
					</Button>
					<Button variant="ghost" href="/items/{item.id}">Cancel</Button>
				</div>
			</Card.Content>
		</Card.Root>
	{/if}
</div>
