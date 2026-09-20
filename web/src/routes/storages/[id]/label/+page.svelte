<script lang="ts">
	import { page } from '$app/state';
	import { getStorage } from '$lib/api';
	import type { Storage } from '$lib/types';
	import LabelSheet from '$lib/components/label-sheet.svelte';
	import { Button } from '$lib/components/ui/button';

	let storage = $state<Storage | null>(null);
	let loadError = $state<string | null>(null);

	// See CLAUDE.md's note on this pattern.
	let loadSeq = 0;
	$effect(() => {
		const id = Number(page.params.id);
		const seq = ++loadSeq;
		storage = null;
		loadError = null;
		getStorage(id)
			.then((r) => {
				if (seq === loadSeq) storage = r.storage;
			})
			.catch((e) => {
				if (seq === loadSeq) loadError = e instanceof Error ? e.message : 'Failed to load storage.';
			});
	});
</script>

<div class="flex flex-col items-center gap-4">
	<h1 class="text-xl font-semibold print:hidden">Print Label</h1>

	{#if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if !storage}
		<p class="text-muted-foreground text-sm print:hidden">Loading…</p>
	{:else}
		<LabelSheet name={storage.name} qrToken={storage.qr_token} />
		<Button onclick={() => window.print()} class="print:hidden">Print</Button>
	{/if}
</div>
