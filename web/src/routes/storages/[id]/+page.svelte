<script lang="ts">
	// Direct children + direct items only (drill-down browsing), not the recursive
	// query from §3 — that one backs the scan flow's "contents view" instead
	// (getStorageContents in $lib/api.ts), where scanning a box should surface
	// everything nested inside it in one shot rather than one level at a time.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { deleteStorage, getItems, getStorage, getStorages, updateStorage } from '$lib/api';
	import { ApiError, type Breadcrumb, type Item, type Location, type Storage } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import PhotoUpload from '$lib/components/photo-upload.svelte';
	import LocationSelect from '$lib/components/location-select.svelte';

	let storage = $state<Storage | null>(null);
	let breadcrumb = $state<Breadcrumb>([]);
	let location = $state<Pick<Location, 'id' | 'name'> | null>(null);
	let children = $state<Storage[]>([]);
	let items = $state<Item[]>([]);
	let loadError = $state<string | null>(null);
	let photoSaveError = $state<string | null>(null);
	let locationSaveError = $state<string | null>(null);

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
		storage = null;
		loadError = null;
		location = null;
		photoSaveError = null;
		locationSaveError = null;
		renaming = false;
		renameError = null;
		savingRename = false;
		deleting = false;
		deleteError = null;
		Promise.all([getStorage(id), getStorages(id), getItems({ storage_id: id })])
			.then(([s, childStorages, storageItems]) => {
				if (seq !== loadSeq) return;
				storage = s.storage;
				breadcrumb = s.breadcrumb;
				location = s.location;
				children = childStorages;
				items = storageItems;
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load storage.';
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
		if (!storage) return;
		renameValue = storage.name;
		renameError = null;
		renaming = true;
	}

	// Kept as its own bindable rather than deriving straight from `location`
	// each render, so LocationSelect can update it optimistically on
	// selection and this can revert it on a failed save.
	let selectedLocationId = $state<number | null>(null);
	$effect(() => {
		selectedLocationId = location?.id ?? null;
	});

	// See items/[id]'s handlePhotoChange — same reasoning: no surrounding
	// edit form to piggyback a save onto, so this PATCHes immediately. Unlike
	// the photo, a location change also changes the breadcrumb (the prepended
	// Location itself), so this re-fetches rather than trusting the PATCH
	// response alone — updateStorage's return value doesn't carry a resolved
	// breadcrumb.
	async function handleLocationChange(newLocationId: number | null) {
		if (!storage) return;
		locationSaveError = null;
		try {
			await updateStorage(storage.id, { location_id: newLocationId });
			const refreshed = await getStorage(storage.id);
			breadcrumb = refreshed.breadcrumb;
			location = refreshed.location;
		} catch (e) {
			locationSaveError = e instanceof Error ? e.message : 'Failed to save location.';
			selectedLocationId = location?.id ?? null; // revert the select
		}
	}

	async function saveRename() {
		if (!storage) return;
		const trimmed = renameValue.trim();
		if (!trimmed) {
			renameError = 'Name cannot be blank.';
			return;
		}
		savingRename = true;
		renameError = null;
		try {
			const updated = await updateStorage(storage.id, { name: trimmed });
			storage = updated;
			renaming = false;
		} catch (e) {
			renameError = e instanceof Error ? e.message : 'Failed to rename storage.';
		} finally {
			savingRename = false;
		}
	}

	// A root storage (no parent) that still directly holds items has nowhere
	// to promote them to — the backend 409s and asks for ?force=true, which
	// deletes those items outright (see internal/api/storages.go's delete
	// handler). Direct items in a non-root storage promote to this
	// storage's own parent, matching where the confirm dialog sends the user
	// afterward — no extra warning needed for those. Child *storages* are a
	// different story: the FK's ON DELETE SET NULL promotes them straight to
	// the root level, not to this storage's parent (verified empirically —
	// see commit message), so without a warning "Shelf 2" just disappears
	// from view (there's no storage-tree browse UI, only search/scan can
	// find a root-level storage again).
	async function handleDelete() {
		if (!storage) return;
		// Only worth flagging as a divergence when this storage itself has a
		// parent — that's the case where a user would otherwise expect children
		// to land there (matching where this storage's own direct items go)
		// but they go to root instead. A root storage's children ending up at
		// root isn't a surprise (there's nowhere else for them to go), so the
		// "not to X" contrast would be confusing (no parent to name) — verified
		// live: deleting a root storage produced "move to the top level, not
		// to the parent" with no parent to refer to before this guard was added.
		let warning = '';
		if (children.length > 0 && storage.parent_id) {
			warning = `\n\n${children.length} storage(s) inside it will move to the top level, not to ${breadcrumb.at(-2)?.name}.`;
		} else if (children.length > 0) {
			warning = `\n\n${children.length} storage(s) inside it will move to the top level.`;
			// A root storage's own location (if any) is exactly what
			// StoragesHandler.delete propagates onto the promoted children —
			// name that here so it doesn't read as data loss (Milestone 2).
			if (location) warning += ` They will keep the "${location.name}" location.`;
		}
		if (!confirm(`Delete "${storage.name}"?${warning}`)) return;
		deleting = true;
		deleteError = null;
		try {
			await deleteStorage(storage.id);
			await goto(storage.parent_id ? `/storages/${storage.parent_id}` : '/');
		} catch (e) {
			if (
				e instanceof ApiError &&
				e.status === 409 &&
				confirm(
					`"${storage.name}" holds items directly and has no parent to promote them to.\n\nDelete it along with everything inside it?`
				)
			) {
				try {
					await deleteStorage(storage.id, { force: true });
					await goto(storage.parent_id ? `/storages/${storage.parent_id}` : '/');
					return;
				} catch (e2) {
					deleteError = e2 instanceof Error ? e2.message : 'Failed to delete storage.';
					deleting = false;
					return;
				}
			}
			deleteError = e instanceof Error ? e.message : 'Failed to delete storage.';
			deleting = false;
		}
	}

	// See items/[id]'s handlePhotoChange — same reasoning: PhotoUpload already
	// persisted the file itself, this just saves the resulting URL.
	async function handlePhotoChange(photoUrl: string | null) {
		if (!storage) return;
		photoSaveError = null;
		try {
			await updateStorage(storage.id, { photo_url: photoUrl });
		} catch (e) {
			photoSaveError = e instanceof Error ? e.message : 'Failed to save photo.';
		}
	}
</script>

<div class="flex flex-col gap-4">
	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !storage}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		<!-- Ancestors only — the current storage gets a real heading below,
		     not a trailing breadcrumb crumb doing double duty as the title.
		     An assigned Location prepends ahead of even the root ancestor
		     (it's "further out" than the storage tree itself) — shown even
		     when there are no storage ancestors at all (a root storage with
		     a location but no parent). -->
		{@const ancestors = breadcrumb.slice(0, -1)}
		{#if ancestors.length > 0 || location}
			<div class="flex flex-wrap items-center gap-1 text-sm">
				{#if location}
					<a href="/locations" class="hover:underline">{location.name}</a>
					{#if ancestors.length > 0}<span class="text-muted-foreground">›</span>{/if}
				{/if}
				{#each ancestors as b, i (b.id)}
					<a href="/storages/{b.id}" class="hover:underline">{b.name}</a>
					{#if i < ancestors.length - 1}<span class="text-muted-foreground">›</span>{/if}
				{/each}
			</div>
		{/if}

		<Card.Root variant="glass" class="p-4">
			<Card.Header class="p-0">
				<div class="flex items-start gap-4">
					<PhotoUpload bind:photoUrl={storage.photo_url} onchange={handlePhotoChange} />
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
							<h1 class="text-xl font-semibold break-words">{storage.name}</h1>
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
						<Button variant="outline" size="sm" href="/storages/{storage.id}/label">Print label</Button>
						<Button variant="ghost" size="sm" onclick={startRename}>Rename</Button>
					{/if}
				</Card.Action>
			</Card.Header>
		</Card.Root>

		{#if storage.parent_id === null}
			<Card.Root variant="glass" class="p-4">
				<Card.Content class="flex flex-col gap-2 p-0">
					<span class="text-sm font-medium">Location</span>
					<LocationSelect bind:locationId={selectedLocationId} onchange={handleLocationChange} />
					{#if locationSaveError}<p class="text-destructive text-xs">{locationSaveError}</p>{/if}
				</Card.Content>
			</Card.Root>
		{/if}

		{#if children.length > 0}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-sm font-medium">Storage</h2>
				{#each children as c (c.id)}
					<a href="/storages/{c.id}" class="block">
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
