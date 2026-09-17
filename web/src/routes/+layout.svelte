<script lang="ts">
	import './layout.css';
	import favicon from '$lib/assets/favicon.svg';
	import SearchBar from '$lib/components/search-bar.svelte';

	let { children } = $props();
</script>

<svelte:head><link rel="icon" href={favicon} /></svelte:head>

<div class="flex min-h-screen flex-col">
	<header class="glass-panel sticky top-0 z-10 flex items-center gap-3 rounded-none border-x-0 border-t-0 px-4 py-3">
		<a href="/" class="shrink-0 text-lg font-semibold">HoardQR</a>
		<SearchBar />
	</header>
	<main class="mx-auto w-full max-w-lg flex-1 p-4">
		{@render children()}
	</main>
</div>
