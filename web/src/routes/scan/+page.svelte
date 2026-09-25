<script lang="ts">
	// /scan (architecture plan §6): two tabs. "Scan code" — four outcomes for a
	// decoded QR/barcode: one item match, several items sharing the code (a short
	// picker), a storage match, or nothing (offer to create new here, pre-filled
	// with the scanned code). "Read label" (added 2026-09-25, user feature request:
	// a plain-text label with no code — e.g. a printed name with nothing else on
	// it — couldn't be scanned by the code tab at all) — OCR the label, then let
	// the unified search resolve it to an item or a storage the same way typing
	// into the Storage Picker's Type tab does, and navigate to whichever it is.
	import { goto } from '$app/navigation';
	import { scan, searchSuggest } from '$lib/api';
	import type { Item, SearchSuggestion } from '$lib/types';
	import QrScanner from '$lib/components/qr-scanner.svelte';
	import LabelOcr from '$lib/components/label-ocr.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';
	import * as Tabs from '$lib/components/ui/tabs';

	let tab = $state<'code' | 'label'>('code');

	// --- Scan code ---
	let resolving = $state(false);
	let pickerItems = $state<Item[] | null>(null);
	let notFoundCode = $state<string | null>(null);
	let scanError = $state<string | null>(null);

	async function handleDecode(code: string) {
		resolving = true;
		pickerItems = null;
		notFoundCode = null;
		scanError = null;
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
			} else if (result.kind === 'storage') {
				await goto(`/storages/${result.storage.id}`);
			} else {
				notFoundCode = code;
			}
		} catch (e) {
			// Without this, a failed /api/scan call (network blip, 500) silently
			// dropped back to the scanner with no indication anything went wrong
			// — the decoded code was just lost.
			scanError = e instanceof Error ? e.message : 'Failed to look up that code.';
		} finally {
			resolving = false;
		}
	}

	function resetCode() {
		pickerItems = null;
		notFoundCode = null;
		scanError = null;
	}

	// --- Read label ---
	let ocrQuery = $state('');
	let ocrSuggestions = $state<SearchSuggestion[]>([]);
	let ocrSearching = $state(false);
	let ocrSearched = $state(false);

	let ocrSearchTimer: ReturnType<typeof setTimeout> | undefined;
	// Sequence guard against stale responses — see CLAUDE.md's note on this pattern.
	let ocrSearchSeq = 0;
	$effect(() => {
		const q = ocrQuery;
		clearTimeout(ocrSearchTimer);
		if (!q.trim()) {
			ocrSuggestions = [];
			ocrSearched = false;
			return;
		}
		ocrSearchTimer = setTimeout(async () => {
			const seq = ++ocrSearchSeq;
			ocrSearching = true;
			try {
				const result = (await searchSuggest(q)).filter((s) => s.kind !== 'tag');
				if (seq === ocrSearchSeq) {
					ocrSuggestions = result;
					ocrSearched = true;
				}
			} catch {
				if (seq === ocrSearchSeq) {
					ocrSuggestions = [];
					ocrSearched = true;
				}
			} finally {
				if (seq === ocrSearchSeq) ocrSearching = false;
			}
		}, 200);
		return () => clearTimeout(ocrSearchTimer);
	});

	function handleRecognized(text: string) {
		// Pre-fill an editable field rather than resolving blind — a misread or an
		// unexpected label layout still lets the user fix it up before it's used
		// to navigate anywhere.
		ocrQuery = text;
	}

	async function selectOcrSuggestion(s: SearchSuggestion) {
		if (s.kind === 'item') {
			await goto(`/items/${s.id}?scanned=1`);
		} else {
			await goto(`/storages/${s.id}`);
		}
	}

	function resetLabel() {
		ocrQuery = '';
		ocrSuggestions = [];
		ocrSearched = false;
	}

	function switchTab(next: 'code' | 'label') {
		tab = next;
		resetCode();
		resetLabel();
	}
</script>

<div class="flex flex-col gap-4">
	<h1 class="text-xl font-semibold">Scan</h1>

	<Tabs.Root value={tab} onValueChange={(v) => switchTab(v as typeof tab)}>
		<Tabs.List>
			<Tabs.Trigger value="code">Scan code</Tabs.Trigger>
			<Tabs.Trigger value="label">Read label</Tabs.Trigger>
		</Tabs.List>

		<Tabs.Content value="code" class="flex flex-col gap-4">
			{#if resolving}
				<p class="text-muted-foreground text-sm">Looking up…</p>
			{:else if scanError}
				<Card.Root variant="glass" class="p-4">
					<Card.Content class="flex flex-col gap-3 p-0">
						<p class="text-destructive text-sm">{scanError}</p>
						<Button variant="ghost" size="sm" class="self-start" onclick={resetCode}>Scan again</Button>
					</Card.Content>
				</Card.Root>
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
						<Button variant="ghost" size="sm" class="self-start" onclick={resetCode}>Scan again</Button>
					</Card.Content>
				</Card.Root>
			{:else if notFoundCode}
				<Card.Root variant="glass" class="p-4">
					<Card.Content class="flex flex-col gap-3 p-0">
						<p class="text-sm">No item or storage uses code "{notFoundCode}".</p>
						<div class="flex flex-wrap gap-2">
							<Button onclick={() => goto(`/items/new?code=${encodeURIComponent(notFoundCode!)}`)}>
								Create item here
							</Button>
							<Button
								variant="outline"
								onclick={() => goto(`/storages/new?code=${encodeURIComponent(notFoundCode!)}`)}
							>
								Create storage here
							</Button>
						</div>
						<Button variant="ghost" size="sm" class="self-start" onclick={resetCode}>Scan again</Button>
					</Card.Content>
				</Card.Root>
			{:else if tab === 'code'}
				<QrScanner onDecode={handleDecode} />
			{/if}
		</Tabs.Content>

		<Tabs.Content value="label" class="flex flex-col gap-3">
			{#if tab === 'label'}
				<LabelOcr onRecognized={handleRecognized} />
			{/if}
			<Input placeholder="Recognized text — edit if needed…" bind:value={ocrQuery} />
			{#if ocrSearching}
				<p class="text-muted-foreground text-sm">Searching…</p>
			{:else if ocrSuggestions.length > 0}
				<ul class="flex flex-col gap-1">
					{#each ocrSuggestions as s (s.kind + s.id)}
						<li>
							<button
								type="button"
								class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
								onclick={() => selectOcrSuggestion(s)}
							>
								{s.name}
								<span class="text-muted-foreground text-xs"> · {s.kind}</span>
								{#if s.breadcrumb}<span class="text-muted-foreground"> — {s.breadcrumb}</span>{/if}
							</button>
						</li>
					{/each}
				</ul>
			{:else if ocrSearched}
				<p class="text-muted-foreground text-sm">No item or storage matches "{ocrQuery}".</p>
			{/if}
		</Tabs.Content>
	</Tabs.Root>
</div>
