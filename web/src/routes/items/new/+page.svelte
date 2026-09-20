<script lang="ts">
	// Add Object flow (architecture plan §8): Storage Picker is required and comes
	// first — you're usually standing next to where the thing lives when you add it.
	// Everything else is optional at creation, editable later.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { createItem, createTag, getStorage, getTags } from '$lib/api';
	import type { Tag } from '$lib/types';
	import StoragePicker from '$lib/components/storage-picker.svelte';
	import PhotoUpload from '$lib/components/photo-upload.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import { cn } from '$lib/utils';

	// Item.condition (types.ts) is plain `string | null` — no DB-level enum
	// (architecture plan §3's `condition TEXT` column has none either) — so
	// this is a frontend-only convenience list, not a constraint the backend
	// will enforce. Free text was tried first and swapped for this per
	// feedback 2026-09-17: a handful of consistent values beats everyone
	// typing their own wording for the same thing.
	const CONDITIONS = ['new', 'good', 'fair', 'poor'];

	// Arrives from /scan's "no match — create here" outcome (§6): the scanned
	// code becomes this item's qr_token instead of generating a new one. Captured
	// once (not $derived) — this route never remounts on a successful create (it
	// navigates away entirely), but a plain $derived would silently pick up a
	// stale ?code= again if that ever changed and make it the next item's code.
	let scannedCode = $state(page.url.searchParams.get('code'));

	let storageId = $state<number | null>(null);
	let breadcrumb = $state('');

	// Arrives from the "+ Add new storage" link's round trip through
	// /storages/new (?returnTo=/items/new) — the newly created storage comes
	// back as ?storageId= instead of making the user pick it again. Fire-and-forget
	// like getTags() below; same one-time-capture reasoning as scannedCode above.
	const returnedStorageId = page.url.searchParams.get('storageId');
	if (returnedStorageId) {
		getStorage(Number(returnedStorageId)).then(({ storage, breadcrumb: path }) => {
			storageId = storage.id;
			breadcrumb = path.map((p) => p.name).join(' > ');
		});
	}

	// One visit to a storage often means storing several different objects
	// (feedback 2026-09-17) — each "draft" is one object's own name/quantity/
	// description/tags, all sharing the single storageId chosen in step 1.
	type Draft = {
		id: number;
		name: string;
		quantity: number;
		description: string;
		// Kept as raw input strings, not Item's typed condition/purchase_date/
		// purchase_price — these are optional (architecture plan §8: everything
		// but storage is optional at creation), so '' is "not set" and parsed
		// to null/number only at submit time rather than forcing a default.
		condition: string;
		purchaseDate: string;
		purchasePrice: string;
		photoUrl: string | null;
		tags: Set<string>;
		tagQuery: string;
		tagOpen: boolean;
	};

	let draftSeq = 0;
	function makeDraft(): Draft {
		return {
			id: draftSeq++,
			name: '',
			quantity: 1,
			description: '',
			condition: '',
			purchaseDate: '',
			purchasePrice: '',
			photoUrl: null,
			tags: new Set(),
			tagQuery: '',
			tagOpen: false
		};
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

	// Empty query still returns a slice of allTags (not []) so focusing the
	// field shows what already exists — the point of adding a text field was
	// to add search on top of browsing, not to replace browsing with it.
	function tagSuggestions(draft: Draft): Tag[] {
		const q = draft.tagQuery.trim().toLowerCase();
		return allTags
			.filter((t) => !draft.tags.has(t.name) && t.name.toLowerCase().includes(q))
			.slice(0, 6);
	}

	function hasExactTagMatch(draft: Draft): boolean {
		const q = draft.tagQuery.trim().toLowerCase();
		return allTags.some((t) => t.name.toLowerCase() === q);
	}

	function selectTag(draft: Draft, tag: Tag) {
		const next = new Set(draft.tags);
		next.add(tag.name);
		draft.tags = next;
		draft.tagQuery = '';
	}

	// Typing a brand-new name and confirming it (Enter, or the "Create" option)
	// persists it via createTag rather than just attaching a bare string to this
	// item — otherwise the tag wouldn't exist for autocomplete on the next draft
	// or the next visit to this page.
	async function confirmTag(draft: Draft) {
		const trimmed = draft.tagQuery.trim();
		if (!trimmed) return;
		const existing = allTags.find((t) => t.name.toLowerCase() === trimmed.toLowerCase());
		if (existing) {
			selectTag(draft, existing);
			return;
		}
		const created = await createTag(trimmed);
		allTags = [...allTags, created];
		selectTag(draft, created);
	}

	function handleTagKeydown(e: KeyboardEvent, draft: Draft) {
		if (e.key === 'Enter') {
			e.preventDefault();
			confirmTag(draft);
		} else if (e.key === 'Escape') {
			draft.tagOpen = false;
		}
	}

	// Same reasoning as search-bar.svelte's handleFocusOut (see CLAUDE.md): a
	// plain onblur would close this before a click on a suggestion button
	// registers, breaking keyboard use entirely. Only close when focus leaves
	// the input+dropdown container, not when it moves within it.
	function handleTagFocusOut(e: FocusEvent, draft: Draft) {
		if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node | null)) {
			draft.tagOpen = false;
		}
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
		storageId = null;
		breadcrumb = '';
		drafts = [makeDraft()];
	}

	async function submit() {
		if (storageId == null) return;
		const targetStorageId = storageId;
		const toCreate = drafts.filter((d) => d.name.trim());
		if (toCreate.length === 0) return;
		submitting = true;
		error = null;
		try {
			const created = [];
			for (const draft of toCreate) {
				const priceInput = draft.purchasePrice.trim();
				const parsedPrice = Number(priceInput);
				created.push(
					await createItem({
						name: draft.name.trim(),
						storage_id: targetStorageId,
						quantity: Number(draft.quantity) || 1,
						description: draft.description.trim() || null,
						condition: draft.condition.trim() || null,
						purchase_date: draft.purchaseDate || null,
						purchase_price: priceInput && !Number.isNaN(parsedPrice) ? parsedPrice : null,
						photo_url: draft.photoUrl,
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
				await goto(`/storages/${targetStorageId}`);
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
				{#if storageId == null}
					<Button
						variant="link"
						size="sm"
						href={`/storages/new?returnTo=${encodeURIComponent(
							scannedCode ? `/items/new?code=${encodeURIComponent(scannedCode)}` : '/items/new'
						)}`}
					>
						+ Add new storage
					</Button>
				{/if}
			</div>
			<StoragePicker bind:storageId bind:breadcrumb />
		</Card.Content>
	</Card.Root>

	{#if storageId != null}
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

						<div class="flex flex-col gap-1.5">
							<span class="text-sm font-medium">Photo</span>
							<PhotoUpload bind:photoUrl={draft.photoUrl} />
						</div>

						<div class="flex flex-col gap-1.5">
							<label for={`condition-${draft.id}`} class="text-sm font-medium">Condition</label>
							<select
								id={`condition-${draft.id}`}
								bind:value={draft.condition}
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
								<label for={`purchase-date-${draft.id}`} class="text-sm font-medium">Purchased</label>
								<Input id={`purchase-date-${draft.id}`} type="date" bind:value={draft.purchaseDate} />
							</div>
							<div class="flex flex-1 flex-col gap-1.5">
								<label for={`purchase-price-${draft.id}`} class="text-sm font-medium">Price</label>
								<Input
									id={`purchase-price-${draft.id}`}
									type="text"
									inputmode="decimal"
									bind:value={draft.purchasePrice}
									placeholder="0.00"
								/>
							</div>
						</div>

						<div class="flex flex-col gap-1.5">
							<label for={`tag-${draft.id}`} class="text-sm font-medium">Tags</label>
							{#if draft.tags.size > 0}
								<div class="flex flex-wrap gap-2">
									{#each [...draft.tags] as tagName (tagName)}
										<button
											type="button"
											class="bg-primary text-primary-foreground flex items-center gap-1 rounded-full px-3 py-1 text-sm"
											onclick={() => toggleTag(draft, tagName)}
										>
											{tagName}
											<span aria-hidden="true">×</span>
										</button>
									{/each}
								</div>
							{/if}
							<div class="relative" onfocusout={(e) => handleTagFocusOut(e, draft)}>
								<Input
									id={`tag-${draft.id}`}
									placeholder="Type to find or create a tag…"
									bind:value={draft.tagQuery}
									onfocus={() => (draft.tagOpen = true)}
									onkeydown={(e) => handleTagKeydown(e, draft)}
								/>
								{#if draft.tagOpen}
									<div class="glass-panel absolute top-full left-0 z-20 mt-1 w-full rounded-md p-1">
										<ul class="flex flex-col gap-0.5">
											{#each tagSuggestions(draft) as t (t.id)}
												<li>
													<button
														type="button"
														class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
														onclick={() => selectTag(draft, t)}
													>
														{t.name}
													</button>
												</li>
											{/each}
											{#if draft.tagQuery.trim() && !hasExactTagMatch(draft)}
												<li>
													<button
														type="button"
														class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
														onclick={() => confirmTag(draft)}
													>
														+ Create "{draft.tagQuery.trim()}"
													</button>
												</li>
											{/if}
										</ul>
									</div>
								{/if}
							</div>
						</div>
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
