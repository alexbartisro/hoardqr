<script lang="ts">
	// Add Object flow (architecture plan §8): Location Picker is required and comes
	// first — you're usually standing next to where the thing lives when you add it.
	// Everything else is optional at creation, editable later.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { createItem, getTags } from '$lib/api';
	import type { Tag } from '$lib/types';
	import LocationPicker from '$lib/components/location-picker.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import { cn } from '$lib/utils';

	// Arrives from /scan's "no match — create here" outcome (§6): the scanned
	// code becomes this item's qr_token instead of generating a new one. Captured
	// once (not $derived) — this route never remounts on a successful create (it
	// navigates away entirely), but a plain $derived would silently pick up a
	// stale ?code= again if that ever changed and make it the next item's code.
	let scannedCode = $state(page.url.searchParams.get('code'));

	let locationId = $state<number | null>(null);
	let breadcrumb = $state('');

	let name = $state('');
	let quantity = $state(1);
	let description = $state('');
	let allTags = $state<Tag[]>([]);
	let selectedTags = $state<Set<string>>(new Set());
	let submitting = $state(false);
	let error = $state<string | null>(null);

	// Static list, fetched once — a plain call, not an $effect (nothing here is
	// reactive, so there's nothing to guard against re-running).
	getTags().then((t) => (allTags = t));

	function toggleTag(tagName: string) {
		const next = new Set(selectedTags);
		if (next.has(tagName)) next.delete(tagName);
		else next.add(tagName);
		selectedTags = next;
	}

	function resetForm() {
		// SvelteKit may reuse this component instance across navigations back to the
		// same route — nothing clears these on its own, so a second add in one
		// session would otherwise start pre-filled with the previous item's data.
		locationId = null;
		breadcrumb = '';
		name = '';
		quantity = 1;
		description = '';
		selectedTags = new Set();
	}

	async function submit() {
		if (locationId == null || !name.trim()) return;
		submitting = true;
		error = null;
		try {
			const created = await createItem({
				name: name.trim(),
				location_id: locationId,
				quantity: Number(quantity) || 1,
				description: description.trim() || null,
				tags: [...selectedTags],
				...(scannedCode ? { qr_token: scannedCode } : {})
			});
			resetForm();
			await goto(`/items/${created.id}`);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to create item.';
		} finally {
			submitting = false;
		}
	}
</script>

<div class="flex flex-col gap-6">
	<h1 class="text-xl font-semibold">Add Object</h1>

	<Card.Root variant="glass" class="p-4">
		<LocationPicker bind:locationId bind:breadcrumb />
	</Card.Root>

	{#if locationId != null}
		<Card.Root variant="glass" class="p-4">
			<Card.Content class="flex flex-col gap-4 p-0">
				{#if scannedCode}
					<p class="text-muted-foreground text-sm">
						Code <span class="font-mono">{scannedCode}</span> from scan will be used for this item.
					</p>
				{/if}

				<div class="flex flex-col gap-1.5">
					<label for="name" class="text-sm font-medium">Name</label>
					<Input id="name" bind:value={name} placeholder="e.g. Multimeter" />
				</div>

				<div class="flex flex-col gap-1.5">
					<label for="quantity" class="text-sm font-medium">Quantity</label>
					<Input id="quantity" type="number" min="1" bind:value={quantity} class="w-24" />
				</div>

				<div class="flex flex-col gap-1.5">
					<label for="description" class="text-sm font-medium">Description</label>
					<textarea
						id="description"
						bind:value={description}
						rows="3"
						class="border-input focus-visible:border-ring focus-visible:ring-ring/50 rounded-md border bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:ring-3"
					></textarea>
				</div>

				{#if allTags.length > 0}
					<div class="flex flex-col gap-1.5">
						<span class="text-sm font-medium">Tags</span>
						<div class="flex flex-wrap gap-2">
							{#each allTags as t (t.id)}
								<button
									type="button"
									class={cn(
										'rounded-full border px-3 py-1 text-sm',
										selectedTags.has(t.name) ? 'bg-primary text-primary-foreground' : 'hover:bg-accent'
									)}
									onclick={() => toggleTag(t.name)}
								>
									{t.name}
								</button>
							{/each}
						</div>
					</div>
				{/if}

				{#if error}<p class="text-destructive text-sm">{error}</p>{/if}

				<Button onclick={submit} disabled={submitting || !name.trim()}>
					{submitting ? 'Adding…' : 'Add Object'}
				</Button>
			</Card.Content>
		</Card.Root>
	{/if}
</div>
