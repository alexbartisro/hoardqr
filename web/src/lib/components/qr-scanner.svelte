<script lang="ts">
	// Shared camera-scanning primitive (html5-qrcode), used by both the Storage
	// Picker's Scan tab and the /scan route (§6) — extracted here once a second
	// consumer needed it, rather than duplicating the html5-qrcode wiring.
	import { Button } from '$lib/components/ui/button';

	let { onDecode }: { onDecode: (code: string) => void } = $props();

	let scanning = $state(false);
	let scanError = $state<string | null>(null);
	const scannerId = `qr-scanner-${Math.random().toString(36).slice(2)}`;
	let html5QrCode: import('html5-qrcode').Html5Qrcode | null = null;

	async function start() {
		scanError = null;
		scanning = true;
		const { Html5Qrcode } = await import('html5-qrcode');
		html5QrCode = new Html5Qrcode(scannerId);
		try {
			await html5QrCode.start(
				{ facingMode: 'environment' },
				{ fps: 10, qrbox: 250 },
				async (decodedText: string) => {
					await stop();
					onDecode(decodedText);
				},
				() => {} // per-frame decode misses are expected noise, not errors
			);
		} catch (e) {
			scanning = false;
			scanError = e instanceof Error ? e.message : 'Could not start camera.';
		}
	}

	async function stop() {
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

	// Callers that conditionally mount this component (e.g. only while a "Scan"
	// tab is active) rely on this cleanup firing on unmount to release the camera.
	$effect(() => {
		return () => {
			stop();
		};
	});
</script>

<div class="flex flex-col gap-2">
	<div id={scannerId} class="mx-auto w-full max-w-xs"></div>
	{#if !scanning}
		<Button onclick={start}>Start camera</Button>
	{:else}
		<Button variant="outline" onclick={stop}>Stop</Button>
	{/if}
	{#if scanError}<p class="text-destructive text-sm">{scanError}</p>{/if}
</div>
