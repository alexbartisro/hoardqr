<script lang="ts">
	// Direct children + direct items only (drill-down browsing), not the recursive
	// query from §3 — that one backs the scan flow's "contents view" instead
	// (getLocationContents in $lib/api.ts), where scanning a box should surface
	// everything nested inside it in one shot rather than one level at a time.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { deleteLocation, getItems, getLocation, getLocations, updateLocation } from '$lib/api';
	import { ApiError, type Breadcrumb, type Item, type Location } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import PhotoUpload from '$lib/components/photo-upload.svelte';

	let location = $state<Location | null>(null);
	let breadcrumb = $state<Breadcrumb>([]);
	let children = $state<Location[]>([]);
	let items = $state<Item[]>([]);
	let loadError = $state<string | null>(null);
	let photoSaveError = $state<string | null>(null);

	let renaming = $state(false);
	let renameValue = $state('');
	let renameError = $state<string | null>(null);
	let savingRename = $state(false);
	let renameInput = $state<HTMLInputElement | null>(null);

	let deleting = $state(false);
	let deleteError = $state<string | null>(null);

	// See items/[id]'s loadSeq comment — same stale-response guard.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		location = null;
		loadError = null;
		renaming = false;
		renameError = null;
		deleteError = null;
		Promise.all([getLocation(id), getLocations(id), getItems({ location_id: id })])
			.then(([loc, childLocations, locItems]) => {
				if (seq !== loadSeq) return;
				location = loc.location;
				breadcrumb = loc.breadcrumb;
				children = childLocations;
				items = locItems;
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load location.';
			});
	});

	// Autofocus + select on entering rename mode, so typing a new name
	// doesn't require an extra click first.
	$effect(() => {
		if (renaming) {
			renameInput?.focus();
			renameInput?.select();
		}
	});

	function startRename() {
		if (!location) return;
		renameValue = location.name;
		renameError = null;
		renaming = true;
	}

	async function saveRename() {
		if (!location) return;
		const trimmed = renameValue.trim();
		if (!trimmed) {
			renameError = 'Name cannot be blank.';
			return;
		}
		savingRename = true;
		renameError = null;
		try {
			const updated = await updateLocation(location.id, { name: trimmed });
			location = updated;
			renaming = false;
		} catch (e) {
			renameError = e instanceof Error ? e.message : 'Failed to rename location.';
		} finally {
			savingRename = false;
		}
	}

	// A root location (no parent) that still directly holds items has nowhere
	// to promote them to — the backend 409s and asks for ?force=true, which
	// deletes those items outright (see internal/api/locations.go's delete
	// handler). Direct items in a non-root location promote to this
	// location's own parent, matching where the confirm dialog sends the user
	// afterward — no extra warning needed for those. Child *locations* are a
	// different story: the FK's ON DELETE SET NULL promotes them straight to
	// the root level, not to this location's parent (verified empirically —
	// see commit message), so without a warning "Shelf 2" just disappears
	// from view (there's no location-tree browse UI, only search/scan can
	// find a root-level location again).
	async function handleDelete() {
		if (!location) return;
		// Only worth flagging as a divergence when this location itself has a
		// parent — that's the case where a user would otherwise expect children
		// to land there (matching where this location's own direct items go)
		// but they go to root instead. A root location's children ending up at
		// root isn't a surprise (there's nowhere else for them to go), so the
		// "not to X" contrast would be confusing (no parent to name) — verified
		// live: deleting a root location produced "move to the top level, not
		// to the parent" with no parent to refer to before this guard was added.
		const warning =
			children.length > 0 && location.parent_id
				? `\n\n${children.length} storage location(s) inside it will move to the top level, not to ${breadcrumb.at(-2)?.name}.`
				: children.length > 0
					? `\n\n${children.length} storage location(s) inside it will move to the top level.`
					: '';
		if (!confirm(`Delete "${location.name}"?${warning}`)) return;
		deleting = true;
		deleteError = null;
		try {
			await deleteLocation(location.id);
			await goto(location.parent_id ? `/locations/${location.parent_id}` : '/');
		} catch (e) {
			if (
				e instanceof ApiError &&
				e.status === 409 &&
				confirm(
					`"${location.name}" holds items directly and has no parent to promote them to.\n\nDelete it along with everything inside it?`
				)
			) {
				try {
					await deleteLocation(location.id, { force: true });
					await goto(location.parent_id ? `/locations/${location.parent_id}` : '/');
					return;
				} catch (e2) {
					deleteError = e2 instanceof Error ? e2.message : 'Failed to delete location.';
					deleting = false;
					return;
				}
			}
			deleteError = e instanceof Error ? e.message : 'Failed to delete location.';
			deleting = false;
		}
	}

	// See items/[id]'s handlePhotoChange — same reasoning: PhotoUpload already
	// persisted the file itself, this just saves the resulting URL.
	async function handlePhotoChange(photoUrl: string | null) {
		if (!location) return;
		photoSaveError = null;
		try {
			await updateLocation(location.id, { photo_url: photoUrl });
		} catch (e) {
			photoSaveError = e instanceof Error ? e.message : 'Failed to save photo.';
		}
	}
</script>

<div class="flex flex-col gap-4">
	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !location}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		<!-- Ancestors only — the current location gets a real heading below,
		     not a trailing breadcrumb crumb doing double duty as the title. -->
		{#if breadcrumb.length > 1}
			{@const ancestors = breadcrumb.slice(0, -1)}
			<div class="flex flex-wrap items-center gap-1 text-sm">
				{#each ancestors as b, i (b.id)}
					<a href="/locations/{b.id}" class="hover:underline">{b.name}</a>
					{#if i < ancestors.length - 1}<span class="text-muted-foreground">›</span>{/if}
				{/each}
			</div>
		{/if}

		<Card.Root variant="glass" class="p-4">
			<Card.Header class="p-0">
				<div class="flex items-start gap-4">
					<PhotoUpload bind:photoUrl={location.photo_url} onchange={handlePhotoChange} />
					<div class="flex min-w-0 flex-1 flex-col gap-1 pt-1">
						{#if renaming}
							<form
								class="flex flex-col gap-1"
								onsubmit={(e) => {
									e.preventDefault();
									saveRename();
								}}
							>
								<Input
									bind:ref={renameInput}
									bind:value={renameValue}
									disabled={savingRename}
									onkeydown={(e) => e.key === 'Escape' && (renaming = false)}
								/>
								{#if renameError}<p class="text-destructive text-xs">{renameError}</p>{/if}
							</form>
						{:else}
							<h1 class="text-xl font-semibold break-words">{location.name}</h1>
						{/if}
						{#if photoSaveError}<p class="text-destructive text-xs">{photoSaveError}</p>{/if}
					</div>
				</div>
				<Card.Action class="flex flex-wrap gap-2">
					{#if renaming}
						<Button size="sm" onclick={saveRename} disabled={savingRename}>
							{savingRename ? 'Saving…' : 'Save'}
						</Button>
						<Button variant="outline" size="sm" onclick={() => (renaming = false)} disabled={savingRename}>
							Cancel
						</Button>
					{:else}
						<Button variant="outline" size="sm" href="/locations/{location.id}/label">Print label</Button>
						<Button variant="ghost" size="sm" onclick={startRename}>Rename</Button>
					{/if}
				</Card.Action>
			</Card.Header>
		</Card.Root>

		{#if children.length > 0}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-sm font-medium">Storage</h2>
				{#each children as c (c.id)}
					<a href="/locations/{c.id}" class="block">
						<Card.Root variant="glass" class="p-3">
							<Card.Content class="p-0 text-sm">{c.name}</Card.Content>
						</Card.Root>
					</a>
				{/each}
			</div>
		{/if}

		{#if items.length > 0}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-sm font-medium">Items</h2>
				{#each items as it (it.id)}
					<a href="/items/{it.id}" class="block">
						<Card.Root variant="glass" class="p-3">
							<Card.Content class="p-0 text-sm">{it.name}</Card.Content>
						</Card.Root>
					</a>
				{/each}
			</div>
		{/if}

		{#if children.length === 0 && items.length === 0}
			<p class="text-muted-foreground text-sm">Nothing here yet.</p>
		{/if}

		<div class="border-border/50 mt-2 flex flex-col items-start gap-1 border-t pt-4">
			<Button variant="destructive" onclick={handleDelete} disabled={deleting}>
				{deleting ? 'Deleting…' : 'Delete'}
			</Button>
			{#if deleteError}<p class="text-destructive text-xs">{deleteError}</p>{/if}
		</div>
	{/if}
</div>
