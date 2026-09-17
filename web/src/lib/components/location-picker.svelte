<script lang="ts">
	// Shared location-resolution component (architecture plan §7), reused by the Add
	// Object / Add Storage flows (Phase 2 step 4) and later /scan. Three input paths,
	// one rule: if what's identified is an item, use its location; if it's a location,
	// use it directly. Only the final "look this code up" call is mocked — camera
	// scanning (html5-qrcode) and OCR (Tesseract.js) are real, client-side, right now.
	import { getLocation, resolveLocation, searchSuggest } from '$lib/api';
	import type { Item, SearchSuggestion } from '$lib/types';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Tabs from '$lib/components/ui/tabs';

	let {
		locationId = $bindable<number | null>(null),
		breadcrumb = $bindable<string>('')
	}: { locationId?: number | null; breadcrumb?: string } = $props();

	let tab = $state<'type' | 'scan' | 'ocr'>('type');

	let query = $state('');
	let suggestions = $state<SearchSuggestion[]>([]);
	let searching = $state(false);

	let pickerItems = $state<Item[] | null>(null); // ambiguous scan/resolve result (§6)
	let resolveError = $state<string | null>(null);

	let scanning = $state(false);
	let scanError = $state<string | null>(null);
	const scannerId = `location-picker-scanner-${Math.random().toString(36).slice(2)}`;
	let html5QrCode: import('html5-qrcode').Html5Qrcode | null = null;

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
			stopScan();
			stopOcrCamera();
		};
	});

	async function resolveTo(id: number) {
		const { location, breadcrumb: path } = await getLocation(id);
		locationId = location.id;
		breadcrumb = path.map((p) => p.name).join(' > ');
		resolveError = null;
		pickerItems = null;
	}

	async function selectSuggestion(s: SearchSuggestion) {
		const id = s.kind === 'item' ? s.location_id! : s.id;
		await resolveTo(id);
		suggestions = [];
		query = '';
	}

	async function handleResolvedCode(code: string) {
		resolveError = null;
		pickerItems = null;
		const result = await resolveLocation(code);
		if (result.kind === 'location') {
			await resolveTo(result.location_id);
		} else if (result.kind === 'items') {
			pickerItems = result.items; // several items share this code — let the user pick
		} else {
			resolveError = `No item or location uses code "${code}".`;
		}
	}

	async function choosePickerItem(item: Item) {
		await resolveTo(item.location_id);
	}

	async function startScan() {
		scanError = null;
		scanning = true;
		const { Html5Qrcode } = await import('html5-qrcode');
		html5QrCode = new Html5Qrcode(scannerId);
		try {
			await html5QrCode.start(
				{ facingMode: 'environment' },
				{ fps: 10, qrbox: 250 },
				async (decodedText: string) => {
					await stopScan();
					await handleResolvedCode(decodedText);
				},
				() => {} // per-frame decode misses are expected noise, not errors
			);
		} catch (e) {
			scanning = false;
			scanError = e instanceof Error ? e.message : 'Could not start camera.';
		}
	}

	async function stopScan() {
		scanning = false;
		if (html5QrCode) {
			try {
				await html5QrCode.stop();
				html5QrCode.clear();
			} catch {
				// already stopped
			}
			html5QrCode = null;
		}
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
		// Both scan and OCR want exclusive camera access — await teardown before the
		// next tab claims the device, or a rapid switch can race two consumers over
		// the same stream.
		if (tab === 'scan' && next !== 'scan') await stopScan();
		if (tab === 'ocr' && next !== 'ocr') stopOcrCamera();
		tab = next;
		if (next === 'ocr') await startOcrCamera();
	}
</script>

<div class="flex flex-col gap-3">
	{#if locationId != null}
		<div class="flex items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
			<span>{breadcrumb}</span>
			<Button
				variant="ghost"
				size="sm"
				onclick={() => {
					locationId = null;
					breadcrumb = '';
				}}
			>
				Change
			</Button>
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
				<div id={scannerId} class="mx-auto w-full max-w-xs"></div>
				{#if !scanning}
					<Button onclick={startScan}>Start camera</Button>
				{:else}
					<Button variant="outline" onclick={stopScan}>Stop</Button>
				{/if}
				{#if scanError}<p class="text-destructive text-sm">{scanError}</p>{/if}
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
