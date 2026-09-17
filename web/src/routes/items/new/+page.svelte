<script lang="ts">
	// Add Object flow (architecture plan §8): Location Picker is required and comes
	// first — you're usually standing next to where the thing lives when you add it.
	// Everything else is optional at creation, editable later.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { createItem, getLocation, getTags } from '$lib/api';
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

	// Arrives from the "+ Add new storage" link's round trip through
	// /locations/new (?returnTo=/items/new) — the newly created location comes
	// back as ?locationId= instead of making the user pick it again. Fire-and-forget
	// like getTags() below; same one-time-capture reasoning as scannedCode above.
	const returnedLocationId = page.url.searchParams.get('locationId');
	if (returnedLocationId) {
		getLocation(Number(returnedLocationId)).then(({ location, breadcrumb: path }) => {
			locationId = location.id;
			breadcrumb = path.map((p) => p.name).join(' > ');
		});
	}

	// One visit to a location often means storing several different objects
	// (feedback 2026-09-17) — each "draft" is one object's own name/quantity/
	// description/tags, all sharing the single locationId chosen in step 1.
	type Draft = {
		id: number;
		name: string;
		quantity: number;
		description: string;
		tags: Set<string>;
	};

	let draftSeq = 0;
	function makeDraft(): Draft {
		return { id: draftSeq++, name: '', quantity: 1, description: '', tags: new Set() };
	}

	// Captured from the plain object before it's wrapped in $state below, not
	// read back out of `drafts` — reading a $state array outside a reactive
	// context only captures its initial value anyway, and this only ever wants
	// the initial value (mirrors scannedCode's one-time-capture reasoning
	// above). Tied to a draft's identity, not its position: toCreate below
	// drops empty drafts before submitting, so "the first draft" and "the
	// first *created* item" aren't the same thing once a draft can be removed
	// or left blank.
	const firstDraft = makeDraft();
	const scannedDraftId = firstDraft.id;
	let drafts = $state<Draft[]>([firstDraft]);
	let allTags = $state<Tag[]>([]);
	let submitting = $state(false);
	let error = $state<string | null>(null);

	// Static list, fetched once — a plain call, not an $effect (nothing here is
	// reactive, so there's nothing to guard against re-running).
	getTags().then((t) => (allTags = t));

	function toggleTag(draft: Draft, tagName: string) {
		const next = new Set(draft.tags);
		if (next.has(tagName)) next.delete(tagName);
		else next.add(tagName);
		draft.tags = next;
	}

	function addMore() {
		drafts.push(makeDraft());
	}

	function removeDraft(id: number) {
		drafts = drafts.filter((d) => d.id !== id);
	}

	function resetForm() {
		// SvelteKit may reuse this component instance across navigations back to the
		// same route — nothing clears these on its own, so a second add in one
		// session would otherwise start pre-filled with the previous item's data.
		locationId = null;
		breadcrumb = '';
		drafts = [makeDraft()];
	}

	async function submit() {
		if (locationId == null) return;
		const targetLocationId = locationId;
		const toCreate = drafts.filter((d) => d.name.trim());
		if (toCreate.length === 0) return;
		submitting = true;
		error = null;
		try {
			const created = [];
			for (const draft of toCreate) {
				created.push(
					await createItem({
						name: draft.name.trim(),
						location_id: targetLocationId,
						quantity: Number(draft.quantity) || 1,
						description: draft.description.trim() || null,
						tags: [...draft.tags],
						// One physical scan corresponds to one physical object, even
						// when several different objects are stored in the same visit
						// — only the draft that was on screen when the code arrived
						// gets it (identity, not position: that draft may no longer
						// be toCreate's first entry if an earlier one was removed).
						...(scannedCode && draft.id === scannedDraftId ? { qr_token: scannedCode } : {})
					})
				);
			}
			resetForm();
			if (created.length === 1) {
				await goto(`/items/${created[0].id}`);
			} else {
				await goto(`/locations/${targetLocationId}`);
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to store object(s).';
		} finally {
			submitting = false;
		}
	}
</script>

<div class="flex flex-col gap-6">
	<h1 class="text-xl font-semibold">Add Object</h1>

	<Card.Root variant="glass" class="p-4">
		<Card.Content class="flex flex-col gap-3 p-0">
			<div class="flex items-center justify-between gap-2">
				<p class="text-sm font-medium">Step 1: choose where you'd like to store it</p>
				{#if locationId == null}
					<Button
						variant="link"
						size="sm"
						href={`/locations/new?returnTo=${encodeURIComponent(
							scannedCode ? `/items/new?code=${encodeURIComponent(scannedCode)}` : '/items/new'
						)}`}
					>
						+ Add new storage
					</Button>
				{/if}
			</div>
			<LocationPicker bind:locationId bind:breadcrumb />
		</Card.Content>
	</Card.Root>

	{#if locationId != null}
		<Card.Root variant="glass" class="p-4">
			<Card.Content class="flex flex-col gap-4 p-0">
				<p class="text-sm font-medium">Step 2: add the details</p>

				{#each drafts as draft, i (draft.id)}
					<div class={cn('flex flex-col gap-4', i > 0 && 'border-t pt-4')}>
						{#if drafts.length > 1}
							<div class="flex items-center justify-between">
								<span class="text-muted-foreground text-xs font-semibold uppercase">
									Object {i + 1}
								</span>
								<Button variant="ghost" size="sm" onclick={() => removeDraft(draft.id)}>
									Remove
								</Button>
							</div>
						{/if}

						{#if draft.id === scannedDraftId && scannedCode}
							<p class="text-muted-foreground text-sm">
								Code <span class="font-mono">{scannedCode}</span> from scan will be used for this item.
							</p>
						{/if}

						<div class="flex flex-col gap-1.5">
							<label for={`name-${draft.id}`} class="text-sm font-medium">Name</label>
							<Input id={`name-${draft.id}`} bind:value={draft.name} placeholder="e.g. Multimeter" />
						</div>

						<div class="flex flex-col gap-1.5">
							<label for={`quantity-${draft.id}`} class="text-sm font-medium">Quantity</label>
							<Input
								id={`quantity-${draft.id}`}
								type="number"
								min="1"
								bind:value={draft.quantity}
								class="w-24"
							/>
						</div>

						<div class="flex flex-col gap-1.5">
							<label for={`description-${draft.id}`} class="text-sm font-medium">Description</label>
							<textarea
								id={`description-${draft.id}`}
								bind:value={draft.description}
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
												draft.tags.has(t.name) ? 'bg-primary text-primary-foreground' : 'hover:bg-accent'
											)}
											onclick={() => toggleTag(draft, t.name)}
										>
											{t.name}
										</button>
									{/each}
								</div>
							</div>
						{/if}
					</div>
				{/each}

				<Button variant="outline" onclick={addMore} disabled={submitting} class="self-start">
					+ Add more
				</Button>

				{#if error}<p class="text-destructive text-sm">{error}</p>{/if}

				<Button onclick={submit} disabled={submitting || !drafts.some((d) => d.name.trim())}>
					{submitting ? 'Storing…' : 'Store Object'}
				</Button>
			</Card.Content>
		</Card.Root>
	{/if}
</div>
