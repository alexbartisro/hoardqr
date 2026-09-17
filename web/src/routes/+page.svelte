<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import Package from '@lucide/svelte/icons/package';
	import FolderPlus from '@lucide/svelte/icons/folder-plus';
</script>

<div class="flex flex-col gap-4">
	<a href="/items/new" class="block">
		<Card.Root variant="glass" class="transition-transform active:scale-[0.98]">
			<Card.Content class="flex flex-row items-center gap-4">
				<Package class="text-primary size-8" />
				<div>
					<Card.Title>Add Object</Card.Title>
					<Card.Description>Catalog something new and place it in a location.</Card.Description>
				</div>
			</Card.Content>
		</Card.Root>
	</a>

	<a href="/locations/new" class="block">
		<Card.Root variant="glass" class="transition-transform active:scale-[0.98]">
			<Card.Content class="flex flex-row items-center gap-4">
				<FolderPlus class="text-primary size-8" />
				<div>
					<Card.Title>Add Storage</Card.Title>
					<Card.Description>Create a new box, shelf, or other storage location.</Card.Description>
				</div>
			</Card.Content>
		</Card.Root>
	</a>
</div>
