<script lang="ts">
	// Shared tag editor — extracted 2026-09-20 from items/new's own inline
	// tag block once the item detail page's Details editor needed the exact
	// same thing a second place ("build it once", same reasoning as
	// storage-picker.svelte/photo-upload.svelte). Bindable `tags: Set<string>`,
	// self-contained: loads the tag list and creates new tags on the fly
	// itself rather than taking them as props, the same shape as
	// location-select.svelte.
	//
	// Fixes a real bug along the way, not just relocates one: the suggestion/
	// create buttons below now have onmousedown={(e) => e.preventDefault()}
	// guarding handleFocusOut — items/new's original inline version didn't,
	// which is the exact Safari click-vs-blur combination (focus-toggled
	// dropdown + onfocusout + a <button> that doesn't take focus on
	// click/tap in Safari) already fixed once in search-bar.svelte (see
	// CLAUDE.md, 2026-09-19) and explicitly not needed by storage-picker.svelte
	// only because its list isn't focus-toggled. This one is, so it needed
	// the same fix, verified against real Safari like the original.
	import { getTags, createTag } from '$lib/api';
	import type { Tag } from '$lib/types';
	import { Input } from '$lib/components/ui/input';

	let { tags = $bindable(), id }: { tags: Set<string>; id: string } = $props();

	let allTags = $state<Tag[]>([]);
	let query = $state('');
	let open = $state(false);

	getTags().then((t) => (allTags = t));

	function toggleTag(tagName: string) {
		const next = new Set(tags);
		if (next.has(tagName)) next.delete(tagName);
		else next.add(tagName);
		tags = next;
	}

	// Empty query still returns a slice of allTags (not []) so focusing the
	// field shows what already exists — the point of adding a text field was
	// to add search on top of browsing, not to replace browsing with it.
	function suggestions(): Tag[] {
		const q = query.trim().toLowerCase();
		return allTags.filter((t) => !tags.has(t.name) && t.name.toLowerCase().includes(q)).slice(0, 6);
	}

	function hasExactMatch(): boolean {
		const q = query.trim().toLowerCase();
		return allTags.some((t) => t.name.toLowerCase() === q);
	}

	function select(tag: Tag) {
		const next = new Set(tags);
		next.add(tag.name);
		tags = next;
		query = '';
	}

	// Typing a brand-new name and confirming it (Enter, or the "Create"
	// option) persists it via createTag rather than just attaching a bare
	// string to this item — otherwise the tag wouldn't exist for
	// autocomplete on the next draft/edit or the next visit to this page.
	async function confirm() {
		const trimmed = query.trim();
		if (!trimmed) return;
		const existing = allTags.find((t) => t.name.toLowerCase() === trimmed.toLowerCase());
		if (existing) {
			select(existing);
			return;
		}
		const created = await createTag(trimmed);
		allTags = [...allTags, created];
		select(created);
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			confirm();
		} else if (e.key === 'Escape') {
			open = false;
		}
	}

	// Same reasoning as search-bar.svelte's handleFocusOut (see CLAUDE.md): a
	// plain onblur would close this before a click on a suggestion button
	// registers, breaking keyboard use entirely. Only close when focus
	// leaves the input+dropdown container, not when it moves within it.
	function handleFocusOut(e: FocusEvent) {
		if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node | null)) {
			open = false;
		}
	}
</script>

<div class="flex flex-col gap-1.5">
	{#if tags.size > 0}
		<div class="flex flex-wrap gap-2">
			{#each [...tags] as tagName (tagName)}
				<button
					type="button"
					class="bg-primary text-primary-foreground flex items-center gap-1 rounded-full px-3 py-1 text-sm"
					onclick={() => toggleTag(tagName)}
				>
					{tagName}
					<span aria-hidden="true">×</span>
				</button>
			{/each}
		</div>
	{/if}
	<div class="relative" onfocusout={handleFocusOut}>
		<Input
			{id}
			placeholder="Type to find or create a tag…"
			bind:value={query}
			onfocus={() => (open = true)}
			onkeydown={handleKeydown}
		/>
		{#if open}
			<div class="glass-panel absolute top-full left-0 z-20 mt-1 w-full rounded-md p-1">
				<ul class="flex flex-col gap-0.5">
					{#each suggestions() as t (t.id)}
						<li>
							<button
								type="button"
								class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
								onmousedown={(e) => e.preventDefault()}
								onclick={() => select(t)}
							>
								{t.name}
							</button>
						</li>
					{/each}
					{#if query.trim() && !hasExactMatch()}
						<li>
							<button
								type="button"
								class="hover:bg-accent w-full rounded-md px-2 py-1.5 text-left text-sm"
								onmousedown={(e) => e.preventDefault()}
								onclick={confirm}
							>
								+ Create "{query.trim()}"
							</button>
						</li>
					{/if}
				</ul>
			</div>
		{/if}
	</div>
</div>
