<script lang="ts">
	// Shared OCR-capture primitive (Tesseract.js), extracted from storage-picker.svelte
	// once /scan's "Read label" tab needed the identical camera+capture+recognize flow
	// (architecture plan §7 originally, now also §6). Self-hosted assets — no runtime
	// CDN dependency (see CLAUDE.md's Tesseract.js note). A caller that wants the
	// camera released when this isn't visible must conditionally *mount* it, not just
	// hide it, the same way QrScanner works — this starts its camera on mount and
	// releases it on unmount, with no separate start/stop API of its own.
	import { Button } from '$lib/components/ui/button';

	let { onRecognized }: { onRecognized: (text: string) => void } = $props();

	let videoEl = $state<HTMLVideoElement | null>(null);
	let stream = $state<MediaStream | null>(null);
	let busy = $state(false);
	let error = $state<string | null>(null);

	async function start() {
		error = null;
		try {
			stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment' } });
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not access camera.';
		}
	}

	function stop() {
		stream?.getTracks().forEach((t) => t.stop());
		stream = null;
	}

	$effect(() => {
		start();
		return () => stop();
	});

	// `videoEl` isn't guaranteed to exist yet the instant start()'s getUserMedia
	// promise resolves — attaching the stream reactively, keyed off both existing,
	// avoids a live stream with no preview and a 0×0 canvas silently fed to Tesseract.
	$effect(() => {
		if (videoEl && stream) videoEl.srcObject = stream;
	});

	async function captureAndRead() {
		if (!videoEl) return;
		busy = true;
		error = null;
		try {
			const canvas = document.createElement('canvas');
			canvas.width = videoEl.videoWidth;
			canvas.height = videoEl.videoHeight;
			canvas.getContext('2d')?.drawImage(videoEl, 0, 0);
			const { default: Tesseract } = await import('tesseract.js');
			const {
				data: { text }
			} = await Tesseract.recognize(canvas, 'eng+ron', {
				// corePath names a specific file to skip SIMD feature-detection; the
				// data packages only publish gzip'd trained data.
				workerPath: '/tesseract/worker.min.js',
				corePath: '/tesseract/tesseract-core-lstm.wasm.js',
				langPath: '/tesseract',
				gzip: true
			});
			// A label's printed name often wraps across lines (verified against a real
			// label: "SORICEI" / "CURELE VELCRO" is one storage, "Soricei Curele Velcro
			// Box") — collapsing to a single space keeps the recognized text matchable
			// as a substring/similarity search, which a literal newline would break.
			const recognized = text.replace(/\s+/g, ' ').trim();
			if (!recognized) {
				error = 'Could not read any text — try again, or type it in below.';
				return;
			}
			onRecognized(recognized);
		} catch (e) {
			error = e instanceof Error ? e.message : 'OCR failed.';
		} finally {
			busy = false;
		}
	}
</script>

<div class="flex flex-col gap-2">
	<!-- svelte-ignore a11y_media_has_caption -->
	<video bind:this={videoEl} autoplay playsinline muted class="mx-auto w-full max-w-xs rounded-md"></video>
	<Button onclick={captureAndRead} disabled={busy || !stream}>
		{busy ? 'Reading…' : 'Capture & read'}
	</Button>
	{#if error}<p class="text-destructive text-sm">{error}</p>{/if}
</div>
