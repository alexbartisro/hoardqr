<script lang="ts">
	// Global search (architecture plan §5) — same searchSuggest() the Location
	// Picker's Type tab and the MCP find_items tool use. Tag hits show for
	// discoverability but aren't clickable yet: there's no tag-filtered browse
	// route (only item/location detail pages exist so far) — rendered as a
	// visually distinct chip, not a row, so it doesn't invite a click.
	import { goto } from '$app/navigation';
	import { searchSuggest } from '$lib/api';
	import type { SearchSuggestion } from '$lib/types';
	import { Input } from '$lib/components/ui/input';

	let query = $state('');
	let suggestions = $state<SearchSuggestion[]>([]);
	let open = $state(false);
	let searching = $state(false);
	// Distinguishes "haven't searched this query yet" (debounce still pending)
	// from "searched it and got nothing" — without this, "No results" flashes
	// for the ~200ms debounce window on every keystroke.
	let searchedQuery = $state('');

	let searchTimer: ReturnType<typeof setTimeout> | undefined;
	// Sequence guard against stale responses — see CLAUDE.md's note on this pattern.
	let searchSeq = 0;
	$effect(() => {
		const q = query;
		clearTimeout(searchTimer);
		if (!q.trim()) {
			suggestions = [];
			return;
		}
		searchTimer = setTimeout(async () => {
			const seq = ++searchSeq;
			searching = true;
			try {
				const result = await searchSuggest(q);
				if (seq === searchSeq) {
					suggestions = result;
					searchedQuery = q;
				}
			} finally {
				if (seq === searchSeq) searching = false;
			}
		}, 200);
		return () => clearTimeout(searchTimer);
	});

	function select(s: SearchSuggestion) {
		if (s.kind === 'tag') return;
		open = false;
		query = '';
		suggestions = [];
		goto(s.kind === 'item' ? `/items/${s.id}` : `/locations/${s.id}`);
	}

	// Closing on a plain `onblur` from the input breaks keyboard use — Tab-ing
	// from the input toward a result button closes the dropdown before the
	// button can receive focus. Checking `relatedTarget` against the container
	// lets focus move between the input and its own results without closing,
	// while still closing when focus leaves to anywhere else (click or Tab).
	let container: HTMLDivElement | undefined;
	function handleFocusOut(e: FocusEvent) {
		if (!container?.contains(e.relatedTarget as Node | null)) open = false;
	}
</script>

<div class="relative ml-auto w-full max-w-xs" bind:this={container} onfocusout={handleFocusOut}>
	<Input placeholder="Search items, locations, tags…" bind:value={query} onfocus={() => (open = true)} />
	{#if open && query.trim()}
		<div class="glass-panel absolute top-full left-0 z-20 mt-1 w-full rounded-md p-1">
			{#if searching || query.trim() !== searchedQuery}
				<p class="text-muted-foreground px-2 py-1.5 text-sm">Searching…</p>
			{:else if suggestions.length === 0}
				<p class="text-muted-foreground px-2 py-1.5 text-sm">No results.</p>
			{:else}
				<ul class="flex flex-col gap-0.5">
					{#each suggestions as s (s.kind + s.id)}
						<li>
							{#if s.kind === 'tag'}
								<span class="bg-muted text-muted-foreground m-1 inline-block rounded-full px-2 py-0.5 text-xs">
									{s.name}
								</span>
							{:else}
								<button
									type="button"
									class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
									onclick={() => select(s)}
								>
									{s.name}
									{#if s.breadcrumb}<span class="text-muted-foreground"> — {s.breadcrumb}</span>{/if}
								</button>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}
</div>
