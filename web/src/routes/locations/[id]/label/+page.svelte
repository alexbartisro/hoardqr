<script lang="ts">
	import { page } from '$app/state';
	import { getLocation } from '$lib/api';
	import type { Location } from '$lib/types';
	import LabelSheet from '$lib/components/label-sheet.svelte';
	import { Button } from '$lib/components/ui/button';

	let location = $state<Location | null>(null);
	let loadError = $state<string | null>(null);

	// See CLAUDE.md's note on this pattern.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		location = null;
		loadError = null;
		getLocation(id)
			.then((r) => {
				if (seq === loadSeq) location = r.location;
			})
			.catch((e) => {
				if (seq === loadSeq) loadError = e instanceof Error ? e.message : 'Failed to load location.';
			});
	});
</script>

<div class="flex flex-col items-center gap-4">
	<h1 class="text-xl font-semibold print:hidden">Print Label</h1>

	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !location}
		<p class="text-muted-foreground text-sm print:hidden">Loading…</p>
	{:else}
		<LabelSheet name={location.name} qrToken={location.qr_token} />
		<Button onclick={() => window.print()} class="print:hidden">Print</Button>
	{/if}
</div>
