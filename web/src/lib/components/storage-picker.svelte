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

	let videoEl = $state<HTMLVideoElement | null>(null);
	let ocrStream = $state<MediaStream | null>(null);
	let ocrBusy = $state(false);
	let ocrError = $state<string | null>(null);

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

	$effect(() => {
		return () => {
			stopOcrCamera();
		};
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

	async function startOcrCamera() {
		ocrError = null;
		try {
			ocrStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' } });
		} catch (e) {
			ocrError = e instanceof Error ? e.message : 'Could not access camera.';
		}
	}

	function stopOcrCamera() {
		ocrStream?.getTracks().forEach((t) => t.stop());
		ocrStream = null;
	}

	// `videoEl` (bound inside Tabs.Content) isn't guaranteed to exist yet the instant
	// startOcrCamera()'s getUserMedia promise resolves — the 'ocr' tab's DOM hasn't
	// necessarily flushed. Attaching the stream reactively, keyed off both existing,
	// avoids a live stream with no preview and a 0×0 canvas silently fed to Tesseract.
	$effect(() => {
		if (videoEl && ocrStream) videoEl.srcObject = ocrStream;
	});

	async function captureAndRead() {
		if (!videoEl) return;
		ocrBusy = true;
		ocrError = null;
		try {
			const canvas = document.createElement('canvas');
			canvas.width = videoEl.videoWidth;
			canvas.height = videoEl.videoHeight;
			canvas.getContext('2d')?.drawImage(videoEl, 0, 0);
			const { default: Tesseract } = await import('tesseract.js');
			const {
				data: { text }
			} = await Tesseract.recognize(canvas, 'eng+ron', {
				// Self-hosted (scripts/vendor-tesseract-assets.mjs) — no runtime CDN
				// dependency. corePath names a specific file to skip SIMD
				// feature-detection; the data packages only publish gzip'd trained data.
				workerPath: '/tesseract/worker.min.js',
				corePath: '/tesseract/tesseract-core-lstm.wasm.js',
				langPath: '/tesseract',
				gzip: true
			});
			const recognized = text.trim();
			if (!recognized) {
				ocrError = 'Could not read any text — try typing instead.';
				return;
			}
			// Recognized text feeds the same resolution path as typing (§7) — the
			// unified search query sorts out on its own whether it's a code or a name.
			query = recognized;
			await switchTab('type'); // not a direct `tab =` assignment — that would skip stopOcrCamera()
		} catch (e) {
			ocrError = e instanceof Error ? e.message : 'OCR failed.';
		} finally {
			ocrBusy = false;
		}
	}

	async function switchTab(next: 'type' | 'scan' | 'ocr') {
		// OCR wants exclusive camera access — await teardown before the next tab
		// claims the device. QrScanner handles its own teardown on unmount (it's
		// only mounted while tab === 'scan', below), so nothing to do for it here.
		if (tab === 'ocr' && next !== 'ocr') stopOcrCamera();
		tab = next;
		if (next === 'ocr') await startOcrCamera();
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
				<!-- svelte-ignore a11y_media_has_caption -->
				<video bind:this={videoEl} autoplay playsinline muted class="mx-auto w-full max-w-xs rounded-md"></video>
				<Button onclick={captureAndRead} disabled={ocrBusy || !ocrStream}>
					{ocrBusy ? 'Reading…' : 'Capture & read'}
				</Button>
				{#if ocrError}<p class="text-destructive text-sm">{ocrError}</p>{/if}
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
