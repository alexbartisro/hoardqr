<script lang="ts">
	// Direct children + direct items only (drill-down browsing), not the recursive
	// query from §3 — that one backs the scan flow's "contents view" instead
	// (getStorageContents in $lib/api.ts), where scanning a box should surface
	// everything nested inside it in one shot rather than one level at a time.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { deleteStorage, getItems, getStorage, getStorages, updateStorage } from '$lib/api';
	import type { Breadcrumb, Item, Location, Storage } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import PhotoUpload from '$lib/components/photo-upload.svelte';
	import LocationSelect from '$lib/components/location-select.svelte';
	import Printer from '@lucide/svelte/icons/printer';
	import Pencil from '@lucide/svelte/icons/pencil';
	import MoveIcon from '@lucide/svelte/icons/move';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import Box from '@lucide/svelte/icons/box';
	import Wrench from '@lucide/svelte/icons/wrench';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';

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

	// Notes editor — draft-copy-then-Save/Cancel, same pattern as the item
	// detail page's Details editor: freeform text shouldn't autosave per
	// keystroke or per blur the way Photo/Location's immediate-onchange
	// controls do.
	let editingNotes = $state(false);
	let draftNotes = $state('');
	let notesError = $state<string | null>(null);
	let savingNotes = $state(false);

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
		editingNotes = false;
		notesError = null;
		savingNotes = false;
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
		// Capture both the target id and the page's current loadSeq before
		// the first await — if the user navigates to a different storage
		// while this save is in flight, `storage`/`location` (and this
		// closure's stale references to them) no longer belong to the page
		// that's now showing, so every write below must check `seq` first
		// rather than trusting `storage` to still mean the same thing.
		const id = storage.id;
		const seq = loadSeq;
		locationSaveError = null;
		try {
			await updateStorage(id, { location_id: newLocationId });
			const refreshed = await getStorage(id);
			if (seq !== loadSeq) return;
			breadcrumb = refreshed.breadcrumb;
			location = refreshed.location;
		} catch (e) {
			if (seq !== loadSeq) return;
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
	// to promote them to — the backend always 409s in that case, with no
	// override (user decision, 2026-09-20: deleting a storage must never
	// delete the items inside it, reversing this handler's old
	// `?force=true` escape hatch — see internal/api/storages.go's delete
	// handler and CLAUDE.md). The 409's own message tells the user to move
	// those items elsewhere first; there's nothing left to retry here.
	// Direct items in a non-root storage promote to this storage's own
	// parent, matching where the confirm dialog sends the user afterward —
	// no extra warning needed for those. Child *storages* are a different
	// story: the FK's ON DELETE SET NULL promotes them straight to the root
	// level, not to this storage's parent (verified empirically — see
	// commit message), so without a warning "Shelf 2" just disappears from
	// view (there's no storage-tree browse UI, only search/scan can find a
	// root-level storage again).
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
			// ApiError's own message already names the 409 case clearly
			// ("...has no parent to promote them to — move them to another
			// storage first, then retry") — no special-casing needed here.
			deleteError = e instanceof Error ? e.message : 'Failed to delete storage.';
			deleting = false;
		}
	}

	function startEditNotes() {
		if (!storage) return;
		draftNotes = storage.notes ?? '';
		notesError = null;
		editingNotes = true;
	}

	async function saveNotes() {
		if (!storage) return;
		const id = storage.id;
		const seq = loadSeq;
		savingNotes = true;
		notesError = null;
		try {
			const updated = await updateStorage(id, { notes: draftNotes.trim() || null });
			if (seq !== loadSeq) return;
			storage = updated;
			editingNotes = false;
		} catch (e) {
			if (seq !== loadSeq) return;
			notesError = e instanceof Error ? e.message : 'Failed to save notes.';
		} finally {
			if (seq === loadSeq) savingNotes = false;
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

	// Shared by all four buttons in the header's action row.
	const actionClass = 'h-auto flex-col gap-1 px-1 py-2 text-xs font-normal';
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

		<!-- Identity card: photo + name only, plus a demoted, divided action
		     strip for Print label/Rename. Deliberately not Card.Action/
		     grid-cols-[1fr_auto] (superseding the 2026-09-19 "the grid is
		     already built in" note for this one case) — that grid is exactly
		     what squeezed the name between two clusters of button chrome,
		     reported live as "still pretty bad" after the first header
		     redesign. Two full-width stacked rows instead: identity on top,
		     secondary actions below a divider, so the name reads as the
		     clear focal point rather than competing with two buttons on the
		     same line. -->
		<Card.Root variant="glass" class="p-4">
			<Card.Header class="p-0">
				<div class="flex min-w-0 items-start gap-3">
					<PhotoUpload bind:photoUrl={storage.photo_url} onchange={handlePhotoChange} />
					<div class="min-w-0 flex-1 pt-0.5">
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
									class="text-base font-semibold"
									onkeydown={(e) => e.key === 'Escape' && (renaming = false)}
								/>
								{#if renameError}<p class="text-destructive text-xs">{renameError}</p>{/if}
							</form>
						{:else}
							<h1 class="text-2xl leading-tight font-bold tracking-tight break-words">{storage.name}</h1>
						{/if}
						{#if photoSaveError}<p class="text-destructive mt-1 text-xs">{photoSaveError}</p>{/if}
					</div>
				</div>
				<!-- Every action on this storage lives in this one row, one style:
				     icon over a short label, four equal columns (fits a 320px
				     screen, where four icon+text ghost buttons side by side
				     don't). Feedback 2026-09-25 — the page previously had four
				     differently-styled buttons in four places (outline "Add
				     photo", ghost Print/Rename, a full-width outline Move and an
				     intrinsic-width Delete in a separate footer). Delete used to
				     be isolated in that footer against mis-clicks; it now sits
				     last, in red, and still goes through confirm(). -->
				<div class="border-border/50 mt-3 border-t pt-3">
					{#if renaming}
						<div class="flex gap-2">
							<Button size="sm" onclick={saveRename} disabled={savingRename}>
								{savingRename ? 'Saving…' : 'Save'}
							</Button>
							<Button variant="outline" size="sm" onclick={() => (renaming = false)} disabled={savingRename}>
								Cancel
							</Button>
						</div>
					{:else}
						<div class="grid grid-cols-4 gap-1">
							<Button variant="ghost" href="/storages/{storage.id}/label" class={actionClass}>
								<Printer class="size-5" /> Label
							</Button>
							<Button variant="ghost" onclick={startRename} class={actionClass}>
								<Pencil class="size-5" /> Rename
							</Button>
							<Button variant="ghost" href="/storages/{storage.id}/move" class={actionClass}>
								<MoveIcon class="size-5" /> Move
							</Button>
							<Button
								variant="ghost"
								onclick={handleDelete}
								disabled={deleting}
								class="{actionClass} text-destructive hover:bg-destructive/10 hover:text-destructive dark:hover:bg-destructive/20"
							>
								<Trash2 class="size-5" />
								{deleting ? 'Deleting…' : 'Delete'}
							</Button>
						</div>
					{/if}
					{#if deleteError}<p class="text-destructive mt-2 text-xs">{deleteError}</p>{/if}
				</div>
			</Card.Header>
		</Card.Root>

		<!-- Details card: Notes always, Location folded in as a root-only
		     subsection below a divider — replaces the old separate Location
		     Card.Root. Keeps this page to the same two-card core (identity,
		     details) as the item detail page, rather than stacking identity +
		     notes + location as three separate glass panels before the
		     children/items lists even start. -->
		<Card.Root variant="glass" class="p-4">
			<div class="flex flex-col gap-2">
				<div class="flex items-center justify-between">
					<span class="text-muted-foreground text-xs font-medium tracking-wide uppercase">Notes</span>
					{#if !editingNotes}
						<Button variant="ghost" size="sm" class="-my-1.5 -mr-2" onclick={startEditNotes}><Pencil /> Edit</Button>
					{/if}
				</div>
				{#if editingNotes}
					<textarea
						bind:value={draftNotes}
						disabled={savingNotes}
						rows="3"
						class="border-input focus-visible:border-ring focus-visible:ring-ring/50 rounded-md border bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:ring-3"
					></textarea>
					{#if notesError}<p class="text-destructive text-xs">{notesError}</p>{/if}
					<div class="flex gap-2">
						<Button size="sm" onclick={saveNotes} disabled={savingNotes}>
							{savingNotes ? 'Saving…' : 'Save'}
						</Button>
						<Button
							variant="outline"
							size="sm"
							onclick={() => (editingNotes = false)}
							disabled={savingNotes}
						>
							Cancel
						</Button>
					</div>
				{:else}
					<p class="text-muted-foreground text-sm">{storage.notes || 'No notes yet.'}</p>
				{/if}
			</div>

			{#if storage.parent_id === null}
				<div class="border-border/50 mt-4 flex flex-col gap-2 border-t pt-4">
					<span class="text-muted-foreground text-xs font-medium tracking-wide uppercase">Location</span>
					<LocationSelect bind:locationId={selectedLocationId} onchange={handleLocationChange} />
					{#if locationSaveError}<p class="text-destructive text-xs">{locationSaveError}</p>{/if}
				</div>
			{/if}
		</Card.Root>

		{#if children.length > 0}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-xs font-medium tracking-wide uppercase">Storage</h2>
				{#each children as c (c.id)}
					<a href="/storages/{c.id}" class="block">
						<Card.Root variant="glass" class="p-3">
							<Card.Content class="flex flex-row items-center gap-2 p-0 text-sm">
								<Box class="text-muted-foreground size-4 shrink-0" />
								<span class="min-w-0 flex-1 truncate">{c.name}</span>
								<ChevronRight class="text-muted-foreground/60 size-4 shrink-0" />
							</Card.Content>
						</Card.Root>
					</a>
				{/each}
			</div>
		{/if}

		{#if items.length > 0}
			<div class="flex flex-col gap-2">
				<h2 class="text-muted-foreground text-xs font-medium tracking-wide uppercase">Items</h2>
				{#each items as it (it.id)}
					<a href="/items/{it.id}" class="block">
						<Card.Root variant="glass" class="p-3">
							<Card.Content class="flex flex-row items-center gap-2 p-0 text-sm">
								<Wrench class="text-muted-foreground size-4 shrink-0" />
								<span class="min-w-0 flex-1 truncate">{it.name}</span>
								<ChevronRight class="text-muted-foreground/60 size-4 shrink-0" />
							</Card.Content>
						</Card.Root>
					</a>
				{/each}
			</div>
		{/if}

		{#if children.length === 0 && items.length === 0}
			<p class="text-muted-foreground text-sm">Nothing here yet.</p>
		{/if}
	{/if}
</div>
