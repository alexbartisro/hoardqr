<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
</script>

<main class="mx-auto flex min-h-screen max-w-md flex-col justify-center gap-6 p-6">
	<Card.Root variant="glass">
		<Card.Header>
			<Card.Title>HoardQR</Card.Title>
			<Card.Description>Base layout sanity check — Phase 2, step 1.</Card.Description>
		</Card.Header>
		<Card.Content>
			<Button>Add Object</Button>
		</Card.Content>
	</Card.Root>
</main>
