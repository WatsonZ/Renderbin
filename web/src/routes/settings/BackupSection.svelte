<script lang="ts">
	// Backup and restore, rendered inline in Settings (super admin only) — the
	// snapshot is the whole database, so it carries every account's files,
	// password hashes and API keys, and restoring it overwrites all of them.
	import Icon from '@iconify/svelte';
	import { t } from '$lib/i18n/index.svelte';
	import { formatSize } from '$lib/format';
	import { AuthApiError } from '$lib/api/auth';
	import { restoreBackup } from '$lib/api/settings';

	let fileInput = $state<HTMLInputElement | null>(null);
	let pending = $state<File | null>(null);
	let busy = $state(false);
	let errorMessage = $state<string | null>(null);
	let done = $state<{ users: number; files: number } | null>(null);

	function onFileSelected(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		pending = input.files?.[0] ?? null;
		errorMessage = null;
		done = null;
		// Cleared so picking the same file twice still fires a change event.
		input.value = '';
	}

	async function confirmRestore() {
		if (!pending || busy) return;
		errorMessage = null;
		busy = true;
		try {
			const result = await restoreBackup(pending);
			done = result;
			pending = null;
			// The restore replaced the sessions table along with everything else,
			// so this session is almost certainly gone and every piece of loaded
			// state is stale. A full reload re-runs the layout's auth guard, which
			// lands on the login page when that is the truth.
			setTimeout(() => location.reload(), 1500);
		} catch (err) {
			// The server describes what it rejected (not a database, no accounts,
			// schema mismatch) and nothing was changed in any of those cases.
			errorMessage =
				err instanceof AuthApiError && err.status === 400 ? err.message : t('error.restore.failed');
		} finally {
			busy = false;
		}
	}
</script>

<section class="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-6">
	<h2 class="flex items-center gap-2 text-base font-semibold">
		<Icon icon="lucide:database-backup" width="17" height="17" class="text-emerald-400" />
		{t('backup.title')}
	</h2>

	<div class="mt-5 flex items-center justify-between gap-4">
		<div>
			<p class="text-sm text-neutral-200">{t('backup.download')}</p>
			<p class="mt-0.5 text-xs text-neutral-500">{t('backup.downloadHint')}</p>
		</div>
		<!-- eslint-disable svelte/no-navigation-without-resolve -- /api/backup is served by the Go backend, not a SvelteKit route -->
		<a
			href="/api/backup"
			download
			class="flex shrink-0 items-center gap-1.5 rounded-lg bg-neutral-800 px-3 py-2 text-sm font-medium text-neutral-200 transition-colors hover:bg-neutral-700"
		>
			<Icon icon="lucide:download" width="15" height="15" />
			{t('backup.downloadButton')}
		</a>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	</div>

	<div class="mt-5 border-t border-neutral-800 pt-5">
		<p class="text-sm text-neutral-200">{t('backup.restore')}</p>
		<p class="mt-0.5 text-xs text-neutral-500">{t('backup.restoreHint')}</p>

		<div class="mt-3 flex flex-wrap items-center gap-2">
			<button
				onclick={() => fileInput?.click()}
				disabled={busy}
				class="flex items-center gap-1.5 rounded-lg bg-neutral-800 px-3 py-2 text-sm font-medium text-neutral-200 transition-colors hover:bg-neutral-700 disabled:opacity-50"
			>
				<Icon icon="lucide:upload" width="15" height="15" />
				{t('backup.chooseFile')}
			</button>
			<input
				bind:this={fileInput}
				type="file"
				accept=".db,application/octet-stream"
				class="hidden"
				onchange={onFileSelected}
			/>
			{#if pending}
				<span class="font-mono text-xs text-neutral-400">
					{pending.name} · {formatSize(pending.size)}
				</span>
			{/if}
		</div>

		{#if pending}
			<!-- A second, deliberate step: this button overwrites every account's
			     data, and the file picker is one misclick. -->
			<div
				class="mt-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-900/50 bg-amber-500/10 px-3 py-2"
			>
				<p class="flex items-start gap-2 text-xs text-amber-300">
					<Icon icon="lucide:alert-triangle" width="14" height="14" class="mt-0.5 shrink-0" />
					{t('backup.restoreWarning')}
				</p>
				<div class="flex shrink-0 items-center gap-2">
					<button
						onclick={() => (pending = null)}
						disabled={busy}
						class="rounded-lg px-3 py-1.5 text-xs font-medium text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100 disabled:opacity-50"
					>
						{t('common.cancel')}
					</button>
					<button
						onclick={confirmRestore}
						disabled={busy}
						class="flex items-center gap-1.5 rounded-lg bg-red-500/90 px-3 py-1.5 text-xs font-medium text-neutral-950 transition-colors hover:bg-red-400 disabled:opacity-50"
					>
						{#if busy}
							<Icon icon="lucide:loader-2" width="13" height="13" class="animate-spin" />
						{/if}
						{busy ? t('backup.restoring') : t('backup.restoreButton')}
					</button>
				</div>
			</div>
		{/if}

		{#if errorMessage}
			<div
				role="alert"
				class="mt-3 flex items-center gap-2 rounded-lg border border-red-900/50 bg-red-500/10 px-3 py-2 text-sm text-red-400"
			>
				<Icon icon="lucide:alert-circle" width="16" height="16" class="shrink-0" />
				{errorMessage}
			</div>
		{/if}
		{#if done}
			<div
				class="mt-3 flex items-center gap-2 rounded-lg border border-emerald-900/50 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-400"
			>
				<Icon icon="lucide:loader-2" width="16" height="16" class="shrink-0 animate-spin" />
				{t('backup.restored', { users: done.users, files: done.files })}
			</div>
		{/if}
	</div>
</section>
