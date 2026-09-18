<script lang="ts">
	// Shared photo-attach component (architecture plan §2/§12), reused by the
	// Add Object / Add Storage flows and the item/location detail pages —
	// same "build it once, don't reimplement per-screen" reasoning as
	// location-picker.svelte. Compresses client-side (resize to 1600px on the
	// longest edge, re-encode to WebP, in a Web Worker) before ever uploading
	// anything — that re-encoding also strips EXIF/GPS along the way, per §12.
	import imageCompression from 'browser-image-compression';
	import { uploadPhoto } from '$lib/api';
	import { Button } from '$lib/components/ui/button';

	let {
		photoUrl = $bindable<string | null>(null),
		onchange
	}: {
		photoUrl?: string | null;
		/** Fired after a successful upload or a removal — callers that need the
		 * change persisted immediately (item/location detail pages, which have
		 * no surrounding form/submit step) do that here rather than this
		 * component knowing anything about items or locations. */
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

<div class="flex flex-col gap-2">
	{#if photoUrl}
		<div class="relative w-28">
			<img src={photoUrl} alt="" class="border-border aspect-square w-28 rounded-md border object-cover" />
			<button
				type="button"
				aria-label="Remove photo"
				onclick={remove}
				class="bg-background/90 text-foreground absolute -top-2 -right-2 flex h-6 w-6 items-center justify-center rounded-full border text-xs shadow-sm"
			>
				×
			</button>
		</div>
	{/if}

	<input
		bind:this={fileInput}
		type="file"
		accept="image/*"
		class="hidden"
		onchange={handleFileChange}
	/>
	<Button
		type="button"
		variant="outline"
		size="sm"
		class="self-start"
		disabled={uploading}
		onclick={() => fileInput?.click()}
	>
		{uploading ? 'Uploading…' : photoUrl ? 'Change photo' : 'Add photo'}
	</Button>

	{#if error}<p class="text-destructive text-xs">{error}</p>{/if}
</div>
