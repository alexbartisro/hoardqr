<script lang="ts">
	// Move a storage — into a different parent storage, or promote it to
	// root under a different Location. Web UI's first affordance for
	// re-parenting (LocationSelect on the detail page already handled
	// reassigning a root storage's Location, but there was no way to nest a
	// storage under a different parent, or promote a nested one back to
	// root, from the UI at all). Same sub-route reasoning as items/[id]/move.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { getStorage, updateStorage } from '$lib/api';
	import type { Location, Storage } from '$lib/types';
	import StoragePicker from '$lib/components/storage-picker.svelte';
	import LocationSelect from '$lib/components/location-select.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';

	let storage = $state<Storage | null>(null);
	// getStorage()'s response carries the resolved Location as a sibling
	// envelope field, not storage.location_name (that's only ever populated
	// by getStorages()'s LEFT JOIN — see types.ts) — so this, not
	// storage.location_name, is what "currently has a location" means here.
	let currentLocation = $state<Pick<Location, 'id' | 'name'> | null>(null);
	let currentPositionText = $state('');
	let loadError = $state<string | null>(null);

	let parentId = $state<number | null>(null);
	let breadcrumb = $state('');
	let locationId = $state<number | null>(null);
	// Captured alongside locationId's initial prefill so `changed` below can
	// tell "still the original location" from "cleared/changed it" — a bare
	// `locationId !== null` check would wrongly report "changed" for a
	// storage that already had a location and the user left untouched.
	let originalLocationId = $state<number | null>(null);
	let submitting = $state(false);
	let error = $state<string | null>(null);

	// See items/[id]'s loadSeq comment — same stale-response guard.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		storage = null;
		loadError = null;
		error = null;
		getStorage(id)
			.then(async (r) => {
				if (seq !== loadSeq) return;
				storage = r.storage;
				currentLocation = r.location;
				currentPositionText =
					[...(r.location ? [r.location.name] : []), ...r.breadcrumb.slice(0, -1).map((b) => b.name)].join(
						' › '
					) || 'the top level';
				parentId = r.storage.parent_id;
				locationId = r.location?.id ?? null;
				originalLocationId = locationId;
				// Prefill the picker on the current parent (if any) so it opens
				// already showing a resolved chip, not an empty picker.
				if (r.storage.parent_id != null) {
					const parent = await getStorage(r.storage.parent_id);
					if (seq !== loadSeq) return;
					breadcrumb = parent.breadcrumb.map((b) => b.name).join(' > ');
				}
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load storage.';
			});
	});

	// A location is only meaningful on a root storage — mirrors the same
	// auto-clear-on-nest $effect in storages/new/+page.svelte.
	$effect(() => {
		if (parentId !== null) locationId = null;
	});

	let changed = $derived(
		!!storage &&
			(parentId !== storage.parent_id || (parentId === null && locationId !== originalLocationId))
	);

	// Losing the current location is a real, surprising side effect worth
	// calling out before submit, not just after — re-parenting a root
	// storage that has a location clears it (a nested storage can't have
	// its own location, see CLAUDE.md's storages_location_only_on_root).
	// Only shown when this move would actually cause it: this storage is
	// currently a root with a location, and the user picked a new parent.
	let willLoseLocation = $derived(
		!!storage && storage.parent_id === null && currentLocation != null && parentId !== null
	);

	async function submit() {
		if (!storage || !changed) return;
		const id = storage.id;
		const seq = loadSeq;
		submitting = true;
		error = null;
		try {
			if (parentId != null) {
				await updateStorage(id, { parent_id: parentId });
			} else {
				await updateStorage(id, { parent_id: null, location_id: locationId });
			}
			if (seq !== loadSeq) return;
			await goto(`/storages/${id}`);
		} catch (e) {
			if (seq !== loadSeq) return;
			error = e instanceof Error ? e.message : 'Failed to move storage.';
		} finally {
			if (seq === loadSeq) submitting = false;
		}
	}
</script>

<div class="flex flex-col gap-6">
	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !storage}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		<div>
			<h1 class="text-xl font-semibold">Move "{storage.name}"</h1>
			<p class="text-muted-foreground text-sm">Currently in {currentPositionText}</p>
		</div>

		<Card.Root variant="glass" class="p-4">
			<StoragePicker bind:storageId={parentId} bind:breadcrumb optional />
		</Card.Root>

		<Card.Root variant="glass" class="p-4">
			<Card.Content class="flex flex-col gap-2 p-0">
				<span class="text-sm font-medium">Location</span>
				{#if parentId == null}
					<LocationSelect bind:locationId />
				{:else}
					<p class="text-muted-foreground text-sm">Inherits its location from {breadcrumb}.</p>
				{/if}
			</Card.Content>
		</Card.Root>

		{#if willLoseLocation}
			<p class="text-muted-foreground text-sm">
				Moving under "{breadcrumb}" will replace its own "{currentLocation?.name}" location with {breadcrumb}'s.
			</p>
		{/if}

		{#if error}<p class="text-destructive text-sm">{error}</p>{/if}
		<div class="flex gap-2">
			<Button onclick={submit} disabled={submitting || !changed}>
				{submitting ? 'Moving…' : 'Move here'}
			</Button>
			<Button variant="ghost" href="/storages/{storage.id}">Cancel</Button>
		</div>
	{/if}
</div>
