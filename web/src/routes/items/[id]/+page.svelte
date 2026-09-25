<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { deleteItem, getItemById, updateItem } from '$lib/api';
	import type { Breadcrumb, Item, Location } from '$lib/types';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import PhotoUpload from '$lib/components/photo-upload.svelte';
	import TagInput from '$lib/components/tag-input.svelte';
	import { CONDITIONS } from '$lib/constants';
	import Printer from '@lucide/svelte/icons/printer';
	import Pencil from '@lucide/svelte/icons/pencil';
	import MoveIcon from '@lucide/svelte/icons/move';
	import Trash2 from '@lucide/svelte/icons/trash-2';

	let item = $state<Item | null>(null);
	let breadcrumb = $state<Breadcrumb>([]);
	let location = $state<Pick<Location, 'id' | 'name'> | null>(null);
	let loadError = $state<string | null>(null);
	let deleting = $state(false);

	let renaming = $state(false);
	let renameValue = $state('');
	let renameError = $state<string | null>(null);
	let savingRename = $state(false);
	let renameInput = $state<HTMLInputElement | null>(null);

	// Details editor (description, quantity, condition, purchase date/price,
	// receipt, tags) — draft copies, not bound straight to `item`, so Cancel
	// can revert without ever having written anything (see PhotoUpload's
	// bind:photoUrl={item.photo_url} above for the contrasting
	// immediate-save case, which has no Cancel because there's nothing to
	// revert). Raw input strings for date/price, same convention as
	// items/new's Draft — parsed to null/number only at save time.
	let editingDetails = $state(false);
	let draftDescription = $state('');
	let draftQuantity = $state(1);
	let draftCondition = $state('');
	let draftPurchaseDate = $state('');
	let draftPurchasePrice = $state('');
	let draftReceiptUrl = $state('');
	let draftTags = $state<Set<string>>(new Set());
	let detailsError = $state<string | null>(null);
	let savingDetails = $state(false);

	// Set by /scan when this page is the result of scanning the item's own code
	// (feedback 2026-09-17): scanning a physical object is someone asking "where
	// does this live", so that page leads with the destination storage instead
	// of item details. $derived, not captured once — unlike items/new's
	// scannedCode (which only ever navigates away after success), this route
	// can be revisited in place for a different id without remounting (see the
	// loadSeq guard below), so a plain one-time read would go stale if the next
	// visit here isn't itself from a scan.
	let cameFromScan = $derived(page.url.searchParams.get('scanned') === '1');

	// Guards against a slower response for a since-abandoned id winning over a
	// faster one for the current id — same pattern as storage-picker.svelte's
	// search debounce, needed anywhere an $effect kicks off a fetch keyed by a
	// value that can change again before that fetch resolves.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		item = null;
		loadError = null;
		photoSaveError = null;
		renaming = false;
		renameError = null;
		savingRename = false;
		deleting = false;
		editingDetails = false;
		detailsError = null;
		savingDetails = false;
		getItemById(id)
			.then((r) => {
				if (seq !== loadSeq) return;
				item = r.item;
				breadcrumb = r.breadcrumb;
				location = r.location;
			})
			.catch((e) => {
				if (seq !== loadSeq) return;
				loadError = e instanceof Error ? e.message : 'Failed to load item.';
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
		if (!item) return;
		renameValue = item.name;
		renameError = null;
		renaming = true;
	}

	async function saveRename() {
		if (!item) return;
		const trimmed = renameValue.trim();
		if (!trimmed) {
			renameError = 'Name cannot be blank.';
			return;
		}
		savingRename = true;
		renameError = null;
		try {
			const updated = await updateItem(item.id, { name: trimmed });
			item = updated;
			renaming = false;
		} catch (e) {
			renameError = e instanceof Error ? e.message : 'Failed to rename item.';
		} finally {
			savingRename = false;
		}
	}

	let photoSaveError = $state<string | null>(null);

	// PhotoUpload already persists the file server-side (POST /api/photos)
	// before this fires — this only saves the resulting URL onto the item
	// itself. There's no surrounding edit form/submit step on this page to
	// piggyback on, so this saves immediately rather than waiting for one.
	async function handlePhotoChange(photoUrl: string | null) {
		if (!item) return;
		photoSaveError = null;
		try {
			await updateItem(item.id, { photo_url: photoUrl });
		} catch (e) {
			photoSaveError = e instanceof Error ? e.message : 'Failed to save photo.';
		}
	}

	function startEditDetails() {
		if (!item) return;
		draftDescription = item.description ?? '';
		draftQuantity = item.quantity;
		draftCondition = item.condition ?? '';
		draftPurchaseDate = item.purchase_date ?? '';
		draftPurchasePrice = item.purchase_price != null ? String(item.purchase_price) : '';
		draftReceiptUrl = item.receipt_url ?? '';
		draftTags = new Set(item.tags);
		detailsError = null;
		editingDetails = true;
	}

	// Same parsing convention as items/new's submit() — empty strings become
	// null, purchase_price parses to a number only when non-empty and valid
	// — so behavior doesn't diverge between create and edit.
	async function saveDetails() {
		if (!item) return;
		// Capture before the await: if the user navigates to a different item
		// while this save is in flight, `item`'s own id no longer belongs to
		// the page now showing — same loadSeq guard reasoning as
		// storages/[id]'s handleLocationChange.
		const id = item.id;
		const seq = loadSeq;
		savingDetails = true;
		detailsError = null;
		try {
			const priceInput = draftPurchasePrice.trim();
			const parsedPrice = Number(priceInput);
			const updated = await updateItem(id, {
				description: draftDescription.trim() || null,
				quantity: Number(draftQuantity) || 1,
				condition: draftCondition.trim() || null,
				purchase_date: draftPurchaseDate || null,
				purchase_price: priceInput && !Number.isNaN(parsedPrice) ? parsedPrice : null,
				receipt_url: draftReceiptUrl.trim() || null,
				tags: [...draftTags]
			});
			if (seq !== loadSeq) return;
			item = updated;
			editingDetails = false;
		} catch (e) {
			if (seq !== loadSeq) return;
			detailsError = e instanceof Error ? e.message : 'Failed to save details.';
		} finally {
			if (seq === loadSeq) savingDetails = false;
		}
	}

	async function handleDelete() {
		if (!item || !confirm(`Delete "${item.name}"?`)) return;
		deleting = true;
		try {
			await deleteItem(item.id);
			await goto(`/storages/${item.storage_id}`);
		} catch (e) {
			loadError = e instanceof Error ? e.message : 'Failed to delete item.';
			deleting = false;
		}
	}

	// Shared by all four buttons in the header's action row.
	const actionClass = 'h-auto flex-col gap-1 px-1 py-2 text-xs font-normal';
</script>

<div class="flex flex-col gap-4">
	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !item}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else}
		{#if cameFromScan}
			{@const ancestorNames = [
				...(location ? [location.name] : []),
				...breadcrumb.slice(0, -1).map((b) => b.name)
			]}
			<Card.Root variant="glass" class="border-primary/50 p-4">
				<Card.Content class="flex flex-col gap-1 p-0">
					<p class="text-muted-foreground text-xs font-medium tracking-wide uppercase">This goes in</p>
					<p class="text-2xl font-semibold">{breadcrumb.at(-1)?.name}</p>
					{#if ancestorNames.length > 0}
						<p class="text-muted-foreground text-sm">{ancestorNames.join(' › ')}</p>
					{/if}
					<a
						href="/storages/{item.storage_id}"
						class="text-primary mt-1 self-start text-sm underline-offset-2 hover:underline"
					>
						View storage →
					</a>
				</Card.Content>
			</Card.Root>
		{:else}
			<div class="flex flex-wrap items-center gap-1 text-sm">
				{#if location}
					<a href="/locations" class="hover:underline">{location.name}</a>
					<span class="text-muted-foreground">›</span>
				{/if}
				{#each breadcrumb as b, i (b.id)}
					<a href="/storages/{b.id}" class="hover:underline">{b.name}</a>
					{#if i < breadcrumb.length - 1}<span class="text-muted-foreground">›</span>{/if}
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
					<PhotoUpload bind:photoUrl={item.photo_url} onchange={handlePhotoChange} />
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
							<h1 class="text-2xl leading-tight font-bold tracking-tight break-words">{item.name}</h1>
						{/if}
						{#if photoSaveError}<p class="text-destructive mt-1 text-xs">{photoSaveError}</p>{/if}
					</div>
				</div>
				<!-- Every action on this item lives in this one row, one style:
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
							<Button variant="ghost" href="/items/{item.id}/label" class={actionClass}>
								<Printer class="size-5" /> Label
							</Button>
							<Button variant="ghost" onclick={startRename} class={actionClass}>
								<Pencil class="size-5" /> Rename
							</Button>
							<Button variant="ghost" href="/items/{item.id}/move" class={actionClass}>
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
				</div>
			</Card.Header>
		</Card.Root>

		<!-- Details card: separate panel from identity, per the same redesign
		     — a title's card shouldn't also carry the growing pile of
		     editable fields added since it was last touched. -->
		<Card.Root variant="glass" class="p-4">
			<div class="flex items-center justify-between">
				<span class="text-muted-foreground text-xs font-medium tracking-wide uppercase">Details</span>
				{#if !editingDetails}
					<Button variant="ghost" size="sm" class="-my-1.5 -mr-2" onclick={startEditDetails}><Pencil /> Edit</Button>
				{/if}
			</div>
			{#if editingDetails}
				<Card.Content class="mt-2 flex flex-col gap-4 p-0 text-sm">
					<div class="flex flex-col gap-1.5">
						<label for="description" class="text-sm font-medium">Description</label>
						<textarea
							id="description"
							bind:value={draftDescription}
							disabled={savingDetails}
							rows="3"
							class="border-input focus-visible:border-ring focus-visible:ring-ring/50 rounded-md border bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:ring-3"
						></textarea>
					</div>
					<div class="flex flex-col gap-1.5">
						<label for="quantity" class="text-sm font-medium">Quantity</label>
						<Input
							id="quantity"
							type="number"
							min="1"
							bind:value={draftQuantity}
							disabled={savingDetails}
							class="w-24"
						/>
					</div>
					<div class="flex flex-col gap-1.5">
						<label for="condition" class="text-sm font-medium">Condition</label>
						<select
							id="condition"
							bind:value={draftCondition}
							disabled={savingDetails}
							class="border-input focus-visible:border-ring focus-visible:ring-ring/50 h-9 rounded-md border bg-transparent px-2.5 text-sm outline-none focus-visible:ring-3"
						>
							<option value="">Not set</option>
							{#each CONDITIONS as c (c)}
								<option value={c}>{c[0].toUpperCase() + c.slice(1)}</option>
							{/each}
						</select>
					</div>
					<div class="flex gap-4">
						<div class="flex flex-1 flex-col gap-1.5">
							<label for="purchase-date" class="text-sm font-medium">Purchased</label>
							<Input
								id="purchase-date"
								type="date"
								bind:value={draftPurchaseDate}
								disabled={savingDetails}
							/>
						</div>
						<div class="flex flex-1 flex-col gap-1.5">
							<label for="purchase-price" class="text-sm font-medium">Price</label>
							<Input
								id="purchase-price"
								type="text"
								inputmode="decimal"
								bind:value={draftPurchasePrice}
								disabled={savingDetails}
								placeholder="0.00"
							/>
						</div>
					</div>
					<div class="flex flex-col gap-1.5">
						<label for="receipt-url" class="text-sm font-medium">Receipt URL</label>
						<Input
							id="receipt-url"
							type="url"
							bind:value={draftReceiptUrl}
							disabled={savingDetails}
							placeholder="https://…"
						/>
					</div>
					<div class="flex flex-col gap-1.5">
						<span class="text-sm font-medium">Tags</span>
						<TagInput id="tags" bind:tags={draftTags} />
					</div>

					{#if detailsError}<p class="text-destructive text-xs">{detailsError}</p>{/if}
					<div class="flex gap-2">
						<Button size="sm" onclick={saveDetails} disabled={savingDetails}>
							{savingDetails ? 'Saving…' : 'Save'}
						</Button>
						<Button
							variant="outline"
							size="sm"
							onclick={() => (editingDetails = false)}
							disabled={savingDetails}
						>
							Cancel
						</Button>
					</div>
				</Card.Content>
			{:else}
				<Card.Content class="mt-2 flex flex-col gap-2 p-0 text-sm">
					{#if item.description}<p class="text-muted-foreground mb-1">{item.description}</p>{/if}
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
					{#if item.receipt_url}
						<div class="flex justify-between">
							<span class="text-muted-foreground">Receipt</span>
							<a
								href={item.receipt_url}
								target="_blank"
								rel="noopener noreferrer"
								class="text-primary hover:underline"
							>
								View receipt →
							</a>
						</div>
					{/if}
					{#if !item.is_shared}
						<p class="text-muted-foreground">Private — not shared</p>
					{/if}
					{#if item.tags.length > 0}
						<div class="mt-2 flex flex-wrap gap-2">
							{#each item.tags as t (t)}
								<span class="bg-secondary text-secondary-foreground rounded-full px-3 py-1 text-xs">{t}</span>
							{/each}
						</div>
					{/if}
				</Card.Content>
			{/if}
		</Card.Root>
	{/if}
</div>
