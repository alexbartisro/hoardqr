<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';
	import { cn } from '$lib/utils';

	let {
		class: className,
		children,
		...rest
	}: HTMLAttributes<HTMLDivElement> & { children: Snippet } = $props();
</script>

<div class={cn('glass-panel rounded-xl', className)} {...rest}>
	{@render children()}
</div>
