<script lang="ts">
	// Add Storage flow (architecture plan §8): Storage Picker here sets an optional
	// parent (a root-level storage like "Balcony" has none). A "generate vs. adopt
	// a barcode" code-assignment UI is deferred to editing after creation (label
	// sheet, still to come) — the one exception is /scan's "no match — create here"
	// outcome (§6), which passes an already-scanned code through via ?code=.
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { createStorage } from '$lib/api';
	import StoragePicker from '$lib/components/storage-picker.svelte';
	import PhotoUpload from '$lib/components/photo-upload.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';

	// Captured once, not $derived — see the same note in items/new/+page.svelte.
	let scannedCode = $state(page.url.searchParams.get('code'));

	// Set when arriving from another create flow's "+ Add new storage" link (e.g.
	// items/new). Only a same-origin relative path is honored — a query param is
	// attacker-controllable, so an absolute/protocol-relative value is ignored
	// rather than used as a redirect target.
	const returnTo = page.url.searchParams.get('returnTo');
	const safeReturnTo = returnTo && returnTo.startsWith('/') && !returnTo.startsWith('//') ? returnTo : null;

	let parentId = $state<number | null>(null);
	let breadcrumb = $state('');

	let name = $state('');
	let photoUrl = $state<string | null>(null);
	let submitting = $state(false);
	let error = $state<string | null>(null);

	function resetForm() {
		// See the same reset in items/new — a second add in one session shouldn't
		// start pre-filled with the previous storage's data.
		parentId = null;
		breadcrumb = '';
		name = '';
		photoUrl = null;
	}

	async function submit() {
		if (!name.trim()) return;
		submitting = true;
		error = null;
		try {
			const created = await createStorage({
				name: name.trim(),
				parent_id: parentId,
				photo_url: photoUrl,
				...(scannedCode ? { qr_token: scannedCode } : {})
			});
			resetForm();
			if (safeReturnTo) {
				const sep = safeReturnTo.includes('?') ? '&' : '?';
				await goto(`${safeReturnTo}${sep}storageId=${created.id}`);
			} else {
				await goto(`/storages/${created.id}`);
			}
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to create storage.';
		} finally {
			submitting = false;
		}
	}
</script>

<div class="flex flex-col gap-6">
	<h1 class="text-xl font-semibold">Add Storage</h1>

	<Card.Root variant="glass" class="p-4">
		<StoragePicker bind:storageId={parentId} bind:breadcrumb optional />
	</Card.Root>

	<Card.Root variant="glass" class="p-4">
		<Card.Content class="flex flex-col gap-4 p-0">
			{#if scannedCode}
				<p class="text-muted-foreground text-sm">
					Code <span class="font-mono">{scannedCode}</span> from scan will be used for this storage.
				</p>
			{/if}

			<div class="flex flex-col gap-1.5">
				<label for="name" class="text-sm font-medium">Name</label>
				<Input id="name" bind:value={name} placeholder="e.g. Storage Cabinet" />
			</div>

			<div class="flex flex-col gap-1.5">
				<span class="text-sm font-medium">Photo</span>
				<PhotoUpload bind:photoUrl />
			</div>

			{#if error}<p class="text-destructive text-sm">{error}</p>{/if}

			<Button onclick={submit} disabled={submitting || !name.trim()}>
				{submitting ? 'Creating…' : 'Add Storage'}
			</Button>
		</Card.Content>
	</Card.Root>
</div>
