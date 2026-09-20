<script lang="ts">
	// Thin wrapper over shadcn/Bits UI's Select (architecture plan §3,
	// Milestone 4) — deliberately not a clone of storage-picker.svelte's
	// Type/Scan/OCR machinery: a Location list is flat and small (a
	// handful of rows at most), so a plain dropdown is the right amount of
	// UI, not an under-used resolution flow built for a numerous, nested,
	// physically-labeled entity.
	import { getLocations } from '$lib/api';
	import type { Location } from '$lib/types';
	import * as Select from '$lib/components/ui/select';

	let {
		locationId = $bindable<number | null>(null),
		onchange
	}: {
		locationId?: number | null;
		/** Fired after a change — same "bindable plus optional onchange" shape
		 * as photo-upload.svelte: Add Storage binds locationId into its own
		 * create-payload state, while the storage detail page (no surrounding
		 * edit form to piggyback a save onto) uses onchange to PATCH the
		 * change through immediately. */
		onchange?: (locationId: number | null) => void;
	} = $props();

	let locations = $state<Location[]>([]);
	let loaded = $state(false);

	$effect(() => {
		getLocations().then((ls) => {
			locations = ls;
			loaded = true;
		});
	});

	// Bits UI's single-select value is a plain string — NONE is a sentinel
	// distinct from any real numeric id (stringified), never itself a valid
	// id, so it can round-trip through the dropdown without colliding.
	const NONE = '__none__';
	let selected = $derived(locationId == null ? NONE : String(locationId));
	let selectedName = $derived(locations.find((l) => l.id === locationId)?.name);

	function handleChange(value: string) {
		locationId = value === NONE ? null : Number(value);
		onchange?.(locationId);
	}
</script>

{#if loaded && locations.length === 0}
	<p class="text-muted-foreground text-sm">
		No locations yet. <a href="/locations" class="text-primary hover:underline">Add one →</a>
	</p>
{:else}
	<Select.Root type="single" value={selected} onValueChange={handleChange}>
		<Select.Trigger class="w-full">
			{selectedName ?? '— No location —'}
		</Select.Trigger>
		<Select.Content>
			<Select.Item value={NONE} label="— No location —">— No location —</Select.Item>
			{#each locations as loc (loc.id)}
				<Select.Item value={String(loc.id)} label={loc.name}>{loc.name}</Select.Item>
			{/each}
		</Select.Content>
	</Select.Root>
{/if}
