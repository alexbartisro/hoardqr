<script lang="ts">
	// Shared storage-resolution component (architecture plan §7), reused by the Add
	// Object / Add Storage flows (Phase 2 step 4) and later /scan. Three input paths,
	// one rule: if what's identified is an item, use its storage; if it's a storage,
	// use it directly. Only the final "look this code up" call is mocked — camera
	// scanning (html5-qrcode) and OCR (Tesseract.js) are real, client-side, right now.
	import { getStorage, resolveStorage, searchSuggest } from '$lib/api';
	import type { Item, SearchSuggestion } from '$lib/types';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Tabs from '$lib/components/ui/tabs';
	import QrScanner from '$lib/components/qr-scanner.svelte';
	import LabelOcr from '$lib/components/label-ocr.svelte';

	let {
		storageId = $bindable<number | null>(null),
		breadcrumb = $bindable<string>(''),
		optional = false
	}: { storageId?: number | null; breadcrumb?: string; optional?: boolean } = $props();

	// `storageId === null` is ambiguous on its own — "not chosen yet" and "no
	// parent, by choice" (Add Storage's root-level case, §8) are different states
	// that need different UI. This tracks the latter explicitly.
	let skipped = $state(false);

	let tab = $state<'type' | 'scan' | 'ocr'>('type');

	let query = $state('');
	let suggestions = $state<SearchSuggestion[]>([]);
	let searching = $state(false);

	let pickerItems = $state<Item[] | null>(null); // ambiguous scan/resolve result (§6)
	let resolveError = $state<string | null>(null);

	let searchTimer: ReturnType<typeof setTimeout> | undefined;
	// Clearing the timer only stops a call that hasn't fired yet — it doesn't cancel
	// one already in flight. Real network responses can still resolve out of order
	// (the mock's uniform delay hides this), so a stale response must be dropped by
	// sequence number rather than trusted just because it's the one that arrived.
	let searchSeq = 0;
	$effect(() => {
		const q = query;
		clearTimeout(searchTimer);
		if (!q.trim()) {
			suggestions = [];
			return;
		}
		searchTimer = setTimeout(async () => {
			const seq = ++searchSeq;
			searching = true;
			try {
				const result = (await searchSuggest(q)).filter((s) => s.kind !== 'tag');
				if (seq === searchSeq) suggestions = result;
			} finally {
				if (seq === searchSeq) searching = false;
			}
		}, 200);
		return () => clearTimeout(searchTimer);
	});

	async function resolveTo(id: number) {
		const { storage, breadcrumb: path } = await getStorage(id);
		storageId = storage.id;
		breadcrumb = path.map((p) => p.name).join(' > ');
		resolveError = null;
		pickerItems = null;
		skipped = false;
	}

	async function selectSuggestion(s: SearchSuggestion) {
		const id = s.kind === 'item' ? s.storage_id! : s.id;
		await resolveTo(id);
		suggestions = [];
		query = '';
	}

	async function handleResolvedCode(code: string) {
		resolveError = null;
		pickerItems = null;
		const result = await resolveStorage(code);
		if (result.kind === 'storage') {
			await resolveTo(result.storage_id);
		} else if (result.kind === 'items') {
			pickerItems = result.items; // several items share this code — let the user pick
		} else {
			resolveError = `No item or storage uses code "${code}".`;
		}
	}

	async function choosePickerItem(item: Item) {
		await resolveTo(item.storage_id);
	}

	function handleOcrRecognized(text: string) {
		// Recognized text feeds the same resolution path as typing (§7) — the
		// unified search query sorts out on its own whether it's a code or a name.
		query = text;
		tab = 'type';
	}

	function switchTab(next: 'type' | 'scan' | 'ocr') {
		tab = next;
	}
</script>

<div class="flex flex-col gap-3">
	{#if storageId != null}
		<div class="flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
			<span>{breadcrumb}</span>
			<Button
				variant="ghost"
				size="sm"
				onclick={() => {
					storageId = null;
					breadcrumb = '';
				}}
			>
				Change
			</Button>
		</div>
	{:else if skipped}
		<div class="flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
			<span class="text-muted-foreground">No parent — root level</span>
			<Button variant="ghost" size="sm" onclick={() => (skipped = false)}>Choose instead</Button>
		</div>
	{:else}
		<Tabs.Root value={tab} onValueChange={(v) => switchTab(v as typeof tab)}>
			<Tabs.List>
				<Tabs.Trigger value="type">Type</Tabs.Trigger>
				<Tabs.Trigger value="scan">Scan</Tabs.Trigger>
				<Tabs.Trigger value="ocr">Read label</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="type" class="flex flex-col gap-2">
				<Input placeholder="Type a name or code…" bind:value={query} />
				{#if searching}
					<p class="text-muted-foreground text-sm">Searching…</p>
				{:else if suggestions.length > 0}
					<ul class="flex flex-col gap-1">
						{#each suggestions as s (s.kind + s.id)}
							<li>
								<button
									type="button"
									class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
									onclick={() => selectSuggestion(s)}
								>
									{s.name}
									{#if s.breadcrumb}
										<span class="text-muted-foreground"> — {s.breadcrumb}</span>
									{/if}
								</button>
							</li>
						{/each}
					</ul>
				{/if}
			</Tabs.Content>

			<Tabs.Content value="scan" class="flex flex-col gap-2">
				{#if tab === 'scan'}
					<QrScanner onDecode={handleResolvedCode} />
				{/if}
			</Tabs.Content>

			<Tabs.Content value="ocr" class="flex flex-col gap-2">
				{#if tab === 'ocr'}
					<LabelOcr onRecognized={handleOcrRecognized} />
				{/if}
			</Tabs.Content>
		</Tabs.Root>

		{#if optional}
			<button
				type="button"
				class="text-muted-foreground self-start text-sm underline-offset-2 hover:underline"
				onclick={() => (skipped = true)}
			>
				No parent — leave at root level
			</button>
		{/if}

		{#if pickerItems}
			<div class="flex flex-col gap-1 rounded-md border p-2">
				<p class="text-muted-foreground text-sm">Several items share this code — which one?</p>
				{#each pickerItems as item (item.id)}
					<button
						type="button"
						class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
						onclick={() => choosePickerItem(item)}
					>
						{item.name}
					</button>
				{/each}
			</div>
		{/if}
		{#if resolveError}<p class="text-destructive text-sm">{resolveError}</p>{/if}
	{/if}
</div>
