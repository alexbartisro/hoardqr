<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import LocationPicker from '$lib/components/location-picker.svelte';

	let locationId = $state<number | null>(null);
	let breadcrumb = $state('');
</script>

<main class="mx-auto flex min-h-screen max-w-md flex-col justify-center gap-6 p-6">
	<Card.Root variant="glass">
		<Card.Header>
			<Card.Title>HoardQR</Card.Title>
			<Card.Description>Location Picker sanity check — Phase 2, step 3.</Card.Description>
		</Card.Header>
		<Card.Content>
			<LocationPicker bind:locationId bind:breadcrumb />
		</Card.Content>
	</Card.Root>
</main>
