<script lang="ts">
	// Shared photo-attach component (architecture plan §2/§12), reused by the
	// Add Object / Add Storage flows and the item/storage detail pages —
	// same "build it once, don't reimplement per-screen" reasoning as
	// storage-picker.svelte. Compresses client-side (resize to 1600px on the
	// longest edge, re-encode to WebP, in a Web Worker) before ever uploading
	// anything — that re-encoding also strips EXIF/GPS along the way, per §12.
	import imageCompression from 'browser-image-compression';
	import { uploadPhoto } from '$lib/api';
	import ImagePlus from '@lucide/svelte/icons/image-plus';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import X from '@lucide/svelte/icons/x';

	let {
		photoUrl = $bindable<string | null>(null),
		onchange
	}: {
		photoUrl?: string | null;
		/** Fired after a successful upload or a removal — callers that need the
		 * change persisted immediately (item/storage detail pages, which have
		 * no surrounding form/submit step) do that here rather than this
		 * component knowing anything about items or storages. */
		onchange?: (photoUrl: string | null) => void;
	} = $props();

	let fileInput = $state<HTMLInputElement | null>(null);
	let uploading = $state(false);
	let error = $state<string | null>(null);

	async function handleFileChange(e: Event) {
		const file = (e.currentTarget as HTMLInputElement).files?.[0];
		if (!file) return;
		uploading = true;
		error = null;
		try {
			const compressed = await imageCompression(file, {
				maxWidthOrHeight: 1600,
				fileType: 'image/webp',
				initialQuality: 0.8,
				useWebWorker: true
			});
			const { photo_url } = await uploadPhoto(compressed);
			photoUrl = photo_url;
			onchange?.(photo_url);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Failed to upload photo.';
		} finally {
			uploading = false;
			// Clears the picked filename so choosing the same file again (e.g.
			// after a failed upload) still fires a change event.
			if (fileInput) fileInput.value = '';
		}
	}

	function remove() {
		// Only clears the bound value — the file itself is left on disk.
		// Deliberate for now: same "don't build for a hypothetical need"
		// reasoning as the rest of this codebase's simplicity choices, not an
		// oversight. Revisit with a real cleanup pass if orphaned uploads ever
		// become a storage concern worth solving.
		photoUrl = null;
		error = null;
		onchange?.(null);
	}
</script>

<!-- The tile itself is the add/change control — no separate "Add photo"
     button beside or below it (feedback 2026-09-25: detail pages had four
     differently-styled buttons in four places, this was one of them). The ×
     is a sibling of the tile button inside a relative wrapper, never nested
     in it: <button> inside <button> is invalid HTML, same class of bug as
     storage-tree-node's <button>-inside-<a>. -->
<div class="flex w-24 shrink-0 flex-col gap-1">
	<div class="relative">
		<button
			type="button"
			aria-label={uploading ? 'Uploading photo' : photoUrl ? 'Change photo' : 'Add photo'}
			disabled={uploading}
			onclick={() => fileInput?.click()}
			class="border-border text-muted-foreground hover:bg-accent/50 focus-visible:ring-ring/50 flex aspect-square w-24 flex-col items-center justify-center gap-1 overflow-hidden rounded-md border text-xs outline-none focus-visible:ring-3 {photoUrl
				? ''
				: 'border-dashed'}"
		>
			{#if photoUrl}
				<img src={photoUrl} alt="" class="h-full w-full object-cover {uploading ? 'opacity-40' : ''}" />
			{:else if uploading}
				<LoaderCircle class="size-5 animate-spin" />
				Uploading…
			{:else}
				<ImagePlus class="size-5" />
				Add photo
			{/if}
		</button>
		{#if photoUrl && uploading}
			<LoaderCircle class="pointer-events-none absolute inset-0 m-auto size-5 animate-spin" />
		{/if}
		{#if photoUrl && !uploading}
			<button
				type="button"
				aria-label="Remove photo"
				onclick={remove}
				class="bg-background/90 text-foreground absolute -top-2 -right-2 flex h-6 w-6 items-center justify-center rounded-full border shadow-sm"
			>
				<X class="size-3.5" />
			</button>
		{/if}
	</div>

	<input
		bind:this={fileInput}
		type="file"
		accept="image/*"
		class="hidden"
		onchange={handleFileChange}
	/>

	{#if error}<p class="text-destructive text-xs">{error}</p>{/if}
</div>
