<script lang="ts">
	// Login form (architecture plan §10) — only active when AUTH_REQUIRED=true,
	// which doesn't exist as a real concept yet. This builds the form against the
	// mock login() (username only, password is ignored by the mock); real
	// sessions — a cookie, actual credential checking — wait for Phase 3.
	import { goto } from '$app/navigation';
	import { login } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import * as Card from '$lib/components/ui/card';

	let username = $state('');
	let password = $state('');
	let submitting = $state(false);
	let error = $state<string | null>(null);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!username.trim() || !password) return;
		submitting = true;
		error = null;
		try {
			await login(username.trim(), password);
			await goto('/');
		} catch (e) {
			error = e instanceof Error ? e.message : 'Login failed.';
		} finally {
			submitting = false;
		}
	}
</script>

<div class="mx-auto flex max-w-sm flex-col gap-6">
	<h1 class="text-center text-xl font-semibold">HoardQR</h1>

	<Card.Root variant="glass" class="p-4">
		<Card.Content class="p-0">
			<form onsubmit={submit} class="flex flex-col gap-4">
				<div class="flex flex-col gap-1.5">
					<label for="username" class="text-sm font-medium">Username</label>
					<Input id="username" bind:value={username} autocomplete="username" />
				</div>

				<div class="flex flex-col gap-1.5">
					<label for="password" class="text-sm font-medium">Password</label>
					<Input id="password" type="password" bind:value={password} autocomplete="current-password" />
				</div>

				{#if error}<p class="text-destructive text-sm">{error}</p>{/if}

				<Button type="submit" disabled={submitting || !username.trim() || !password}>
					{submitting ? 'Logging in…' : 'Log in'}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>
