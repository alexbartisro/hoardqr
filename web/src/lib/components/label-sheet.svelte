<script lang="ts">
	// Printable label (architecture plan §4) — renders a qr_token as either a QR
	// code or a Code128 barcode, client-side, straight from the string. The
	// backend never generates or stores an image; it only ever holds this text.
	import { Button } from '$lib/components/ui/button';

	let { name, qrToken }: { name: string; qrToken: string } = $props();

	let format = $state<'qr' | 'barcode'>('qr');
	let qrCanvas = $state<HTMLCanvasElement | null>(null);
	let barcodeSvg = $state<SVGSVGElement | null>(null);
	let renderError = $state<string | null>(null);

	// Both effects kick off a dynamic import() keyed on `format` (and `qrToken`,
	// read implicitly) — same stale-response risk as any other keyed fetch (see
	// CLAUDE.md): toggling formats quickly, or a slow chunk load, could otherwise
	// render into a canvas/svg that's since been swapped out.
	let renderSeq = 0;

	$effect(() => {
		if (format !== 'qr' || !qrCanvas) return;
		const seq = ++renderSeq;
		const canvas = qrCanvas;
		renderError = null;
		import('qrcode')
			.then(({ default: QRCode }) => {
				if (seq !== renderSeq) return;
				return QRCode.toCanvas(canvas, qrToken, { width: 220, margin: 1 });
			})
			.catch((e) => {
				if (seq !== renderSeq) return;
				renderError = e instanceof Error ? e.message : 'Could not render QR code.';
			});
	});

	$effect(() => {
		if (format !== 'barcode' || !barcodeSvg) return;
		const seq = ++renderSeq;
		const svg = barcodeSvg;
		renderError = null;
		import('jsbarcode')
			.then(({ default: JsBarcode }) => {
				if (seq !== renderSeq) return;
				svg.replaceChildren(); // JsBarcode appends rather than replacing
				JsBarcode(svg, qrToken, { format: 'CODE128', displayValue: false, margin: 8 });
			})
			.catch((e) => {
				if (seq !== renderSeq) return;
				renderError = e instanceof Error ? e.message : 'Could not render barcode.';
			});
	});
</script>

<div class="flex flex-col items-center gap-3 print:text-black">
	<div class="flex gap-2 print:hidden">
		<Button variant={format === 'qr' ? 'default' : 'outline'} size="sm" onclick={() => (format = 'qr')}>
			QR code
		</Button>
		<Button variant={format === 'barcode' ? 'default' : 'outline'} size="sm" onclick={() => (format = 'barcode')}>
			Barcode
		</Button>
	</div>

	<div class="flex flex-col items-center gap-2 rounded-md border p-6 print:border-0 print:p-0">
		{#if format === 'qr'}
			<canvas bind:this={qrCanvas}></canvas>
		{:else}
			<svg bind:this={barcodeSvg}></svg>
		{/if}
		<p class="text-center text-sm font-medium print:text-black">{name}</p>
		<p class="text-muted-foreground text-center font-mono text-xs print:text-black">{qrToken}</p>
	</div>

	{#if renderError}<p class="text-destructive text-sm">{renderError}</p>{/if}
</div>
