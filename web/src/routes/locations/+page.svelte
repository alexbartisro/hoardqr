<script lang="ts">
	// Manage Locations (architecture plan §3, Milestone 4) — a 2–6-row
	// single-field entity doesn't need its own detail/new routes, unlike
	// storages: this one page does list/create/rename/delete inline.
	import { getLocations, getStorages, createLocation, updateLocation, deleteLocation } from '$lib/api';
	import { ApiError, type Location } from '$lib/types';
	import * as Card from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';

	let locations = $state<Location[]>([]);
	// Storage counts come from the existing root-storages list (each row
	// already carries location_id) rather than one getLocation(id) call per
	// row — a handful of locations doesn't justify an N+1 fetch pattern.
	let counts = $state<Record<number, number>>({});
	let loading = $state(true);
	let loadError = $state<string | null>(null);

	async function load() {
		loading = true;
		loadError = null;
		try {
			const [locs, roots] = await Promise.all([getLocations(), getStorages()]);
			locations = locs;
			const c: Record<number, number> = {};
			for (const s of roots) {
				if (s.location_id != null) c[s.location_id] = (c[s.location_id] ?? 0) + 1;
			}
			counts = c;
		} catch (e) {
			loadError = e instanceof Error ? e.message : 'Failed to load locations.';
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		load();
	});

	let newName = $state('');
	let creating = $state(false);
	let createError = $state<string | null>(null);

	async function handleCreate() {
		const name = newName.trim();
		if (!name) return;
		creating = true;
		createError = null;
		try {
			await createLocation({ name });
			newName = '';
			await load();
		} catch (e) {
			createError = e instanceof Error ? e.message : 'Failed to create location.';
		} finally {
			creating = false;
		}
	}

	let renamingId = $state<number | null>(null);
	let renameValue = $state('');
	let renameError = $state<string | null>(null);
	let savingRename = $state(false);

	function startRename(loc: Location) {
		renamingId = loc.id;
		renameValue = loc.name;
		renameError = null;
	}

	async function saveRename(id: number) {
		const name = renameValue.trim();
		if (!name) {
			renameError = 'Name cannot be blank.';
			return;
		}
		savingRename = true;
		renameError = null;
		try {
			await updateLocation(id, { name });
			renamingId = null;
			await load();
		} catch (e) {
			renameError = e instanceof Error ? e.message : 'Failed to rename location.';
		} finally {
			savingRename = false;
		}
	}

	let deletingId = $state<number | null>(null);
	let deleteError = $state<string | null>(null);

	async function handleDelete(loc: Location) {
		if (!confirm(`Delete "${loc.name}"?`)) return;
		deletingId = loc.id;
		deleteError = null;
		try {
			await deleteLocation(loc.id);
			await load();
		} catch (e) {
			if (
				e instanceof ApiError &&
				e.status === 409 &&
				confirm(`${e.message}\n\nDelete anyway? Its storages will become unassigned, not deleted.`)
			) {
				try {
					await deleteLocation(loc.id, { force: true });
					await load();
					return;
				} catch (e2) {
					deleteError = e2 instanceof Error ? e2.message : 'Failed to delete location.';
					return;
				}
			}
			deleteError = e instanceof Error ? e.message : 'Failed to delete location.';
		} finally {
			deletingId = null;
		}
	}
</script>

<div class="flex flex-col gap-4">
	<h1 class="text-xl font-semibold">Locations</h1>
	<p class="text-muted-foreground text-sm">
		Physical properties (a house, garage, etc.) that a root-level storage can optionally belong to.
	</p>

	<Card.Root variant="glass" class="p-4">
		<Card.Content class="flex flex-col gap-2 p-0">
			<form
				class="flex gap-2"
				onsubmit={(e) => {
					e.preventDefault();
					handleCreate();
				}}
			>
				<Input bind:value={newName} placeholder="e.g. House" disabled={creating} />
				<Button type="submit" disabled={creating || !newName.trim()}>
					{creating ? 'Adding…' : '+ Add location'}
				</Button>
			</form>
			{#if createError}<p class="text-destructive text-sm">{createError}</p>{/if}
		</Card.Content>
	</Card.Root>

	{#if loading}
		<p class="text-muted-foreground text-sm">Loading…</p>
	{:else if loadError}
		<p class="text-destructive text-sm">{loadError}</p>
	{:else if locations.length === 0}
		<p class="text-muted-foreground text-sm">No locations yet.</p>
	{:else}
		<div class="flex flex-col gap-2">
			{#each locations as loc (loc.id)}
				<Card.Root variant="glass" class="p-3">
					<Card.Content class="flex flex-row items-center justify-between gap-2 p-0">
						{#if renamingId === loc.id}
							<form
								class="flex min-w-0 flex-1 flex-col gap-1"
								onsubmit={(e) => {
									e.preventDefault();
									saveRename(loc.id);
								}}
							>
								<Input
									bind:value={renameValue}
									disabled={savingRename}
									onkeydown={(e) => e.key === 'Escape' && (renamingId = null)}
								/>
								{#if renameError}<p class="text-destructive text-xs">{renameError}</p>{/if}
							</form>
							<div class="flex shrink-0 gap-2">
								<Button size="sm" onclick={() => saveRename(loc.id)} disabled={savingRename}>
									{savingRename ? 'Saving…' : 'Save'}
								</Button>
								<Button variant="outline" size="sm" onclick={() => (renamingId = null)} disabled={savingRename}>
									Cancel
								</Button>
							</div>
						{:else}
							<div class="flex min-w-0 flex-1 flex-col">
								<span class="truncate font-medium">{loc.name}</span>
								<span class="text-muted-foreground text-xs">
									{counts[loc.id] ?? 0} storage{counts[loc.id] === 1 ? '' : 's'}
								</span>
							</div>
							<div class="flex shrink-0 gap-2">
								<Button variant="ghost" size="sm" onclick={() => startRename(loc)}>Rename</Button>
								<Button
									variant="destructive"
									size="sm"
									onclick={() => handleDelete(loc)}
									disabled={deletingId === loc.id}
								>
									{deletingId === loc.id ? 'Deleting…' : 'Delete'}
								</Button>
							</div>
						{/if}
					</Card.Content>
				</Card.Root>
			{/each}
		</div>
		{#if deleteError}<p class="text-destructive text-sm">{deleteError}</p>{/if}
	{/if}
</div>
