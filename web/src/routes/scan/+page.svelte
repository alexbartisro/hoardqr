<script lang="ts">
	// /scan (architecture plan §6): four outcomes for a decoded code — one item
	// match, several items sharing the code (a short picker), a location match,
	// or nothing (offer to create new here, pre-filled with the scanned code).
	import { goto } from '$app/navigation';
	import { scan } from '$lib/api';
	import type { Item } from '$lib/types';
	import QrScanner from '$lib/components/qr-scanner.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';

	let resolving = $state(false);
	let pickerItems = $state<Item[] | null>(null);
	let notFoundCode = $state<string | null>(null);

	async function handleDecode(code: string) {
		resolving = true;
		pickerItems = null;
		notFoundCode = null;
		try {
			const result = await scan(code);
			if (result.kind === 'item') {
				if (result.items.length === 1) {
					// ?scanned=1 tells the item page to lead with where the item goes
					// (feedback 2026-09-17) — the whole point of scanning a physical
					// object is "where does this live", not the item's own details.
					await goto(`/items/${result.items[0].id}?scanned=1`);
				} else {
					pickerItems = result.items; // several items share this code — let the user pick
				}
			} else if (result.kind === 'location') {
				await goto(`/locations/${result.location.id}`);
			} else {
				notFoundCode = code;
			}
		} finally {
			resolving = false;
		}
	}

	function reset() {
		pickerItems = null;
		notFoundCode = null;
	}
</script>

<div class="flex flex-col gap-4">
	<h1 class="text-xl font-semibold">Scan</h1>

	{#if resolving}
		<p class="text-muted-foreground text-sm">Looking up…</p>
	{:else if pickerItems}
		<Card.Root variant="glass" class="p-4">
			<Card.Content class="flex flex-col gap-1 p-0">
				<p class="text-muted-foreground text-sm">Several items share this code — which one?</p>
				{#each pickerItems as item (item.id)}
					<a
						href="/items/{item.id}?scanned=1"
						class="hover:bg-accent block rounded-md px-2 py-1.5 text-left text-sm"
					>
						{item.name}
					</a>
				{/each}
				<Button variant="ghost" size="sm" class="self-start" onclick={reset}>Scan again</Button>
			</Card.Content>
		</Card.Root>
	{:else if notFoundCode}
		<Card.Root variant="glass" class="p-4">
			<Card.Content class="flex flex-col gap-3 p-0">
				<p class="text-sm">No item or location uses code "{notFoundCode}".</p>
				<div class="flex flex-wrap gap-2">
					<Button onclick={() => goto(`/items/new?code=${encodeURIComponent(notFoundCode!)}`)}>
						Create item here
					</Button>
					<Button variant="outline" onclick={() => goto(`/locations/new?code=${encodeURIComponent(notFoundCode!)}`)}>
						Create storage here
					</Button>
				</div>
				<Button variant="ghost" size="sm" class="self-start" onclick={reset}>Scan again</Button>
			</Card.Content>
		</Card.Root>
	{:else}
		<QrScanner onDecode={handleDecode} />
	{/if}
</div>
