<script lang="ts">
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';

	let { children } = $props();
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>

<div class="flex min-h-screen flex-col">
	<header class="glass-panel sticky top-0 z-10 flex items-center gap-2 rounded-none border-x-0 border-t-0 px-4 py-3">
		<a href="/" class="text-lg font-semibold">HoardQR</a>
	</header>
	<main class="mx-auto w-full max-w-lg flex-1 p-4">
		{@render children()}
	</main>
</div>
