<script lang="ts">
	// Account management, rendered inline in Settings (super admin only). Lives
	// beside +page.svelte rather than in $lib/components because it is one page's
	// section, not shared UI — SvelteKit only treats +-prefixed files as routes,
	// so a plain component here is not reachable as a URL.
	import Icon from '@iconify/svelte';
	import { t, intlLocale } from '$lib/i18n/index.svelte';
	import {
		createUser,
		deleteUser,
		resetUserPassword,
		setUserDisabled,
		setUserQuota,
		type AdminUser,
		type CreatedUser
	} from '$lib/api/admin';
	import { copyText } from '$lib/clipboard';
	import { formatSize, parseSize } from '$lib/format';

	let { users: initial }: { users: AdminUser[] } = $props();

	// Rows are mutated locally after each successful call: the status endpoint
	// answers 204, since the row it could return wouldn't carry the file counts
	// shown here.
	// svelte-ignore state_referenced_locally
	let users = $state<AdminUser[]>(initial);

	let errorMessage = $state<string | null>(null);
	let successMessage = $state<string | null>(null);
	let busyId = $state<number | null>(null);

	// Reset-password modal.
	let resetUser = $state<AdminUser | null>(null);
	let resetPassword = $state('');
	let resetBusy = $state(false);
	let resetError = $state<string | null>(null);

	// Create-account modal, and the credentials it hands back exactly once.
	let createOpen = $state(false);
	let createUsername = $state('');
	let createNickname = $state('');
	let createBusy = $state(false);
	let createError = $state<string | null>(null);
	let createdUser = $state<CreatedUser | null>(null);
	let createdCopied = $state(false);

	// Delete-account modal. Deleting takes the account's files with it and does
	// not go through the trash, so this is a modal spelling out the counts
	// rather than a one-line confirm().
	let deleteUserTarget = $state<AdminUser | null>(null);
	let deleteBusy = $state(false);
	let deleteError = $state<string | null>(null);

	// Inline quota editing, one row at a time.
	let quotaEditId = $state<number | null>(null);
	let quotaInput = $state('');
	let quotaBusy = $state(false);

	const MIN_PASSWORD = 6;

	function formatDate(iso: string): string {
		return new Date(iso).toLocaleDateString(intlLocale(), {
			year: 'numeric',
			month: 'short',
			day: 'numeric'
		});
	}

	async function toggleDisabled(user: AdminUser) {
		// Disabling signs someone out and breaks their links, so it asks first;
		// re-enabling only restores access and needs no confirmation.
		if (!user.disabled && !confirm(t('confirm.disableAccount', { name: user.nickname }))) return;
		errorMessage = null;
		successMessage = null;
		busyId = user.id;
		try {
			await setUserDisabled(user.id, !user.disabled);
			users = users.map((u) => (u.id === user.id ? { ...u, disabled: !user.disabled } : u));
		} catch {
			errorMessage = t('error.accountStatus');
		} finally {
			busyId = null;
		}
	}

	function openReset(user: AdminUser) {
		resetUser = user;
		resetPassword = '';
		resetError = null;
	}

	function closeReset() {
		resetUser = null;
		resetPassword = '';
	}

	async function submitReset() {
		if (!resetUser) return;
		if (resetPassword.length < MIN_PASSWORD) {
			resetError = t('register.passwordTooShort');
			return;
		}
		resetError = null;
		resetBusy = true;
		const name = resetUser.nickname;
		try {
			await resetUserPassword(resetUser.id, resetPassword);
			successMessage = t('accounts.resetDone', { name });
			errorMessage = null;
			closeReset();
		} catch (err) {
			resetError = err instanceof Error ? err.message : t('error.resetPassword');
		} finally {
			resetBusy = false;
		}
	}

	function openCreate() {
		createOpen = true;
		createUsername = '';
		createNickname = '';
		createError = null;
		createdUser = null;
		createdCopied = false;
	}

	function closeCreate() {
		createOpen = false;
		createdUser = null;
	}

	async function submitCreate() {
		const username = createUsername.trim();
		const nickname = createNickname.trim() || username;
		if (!username) {
			createError = t('accounts.usernameRequired');
			return;
		}
		createError = null;
		createBusy = true;
		try {
			const created = await createUser(username, nickname);
			// The password is in this response and nowhere else, so the modal
			// switches to showing it instead of closing.
			createdUser = created;
			users = [
				...users,
				{
					id: created.id,
					username: created.username,
					nickname: created.nickname,
					is_super_admin: false,
					disabled: false,
					disabled_at: null,
					created_at: new Date().toISOString(),
					file_count: 0,
					trashed_count: 0,
					used_bytes: 0,
					// Mirrors the server's default; the row re-reads it on the
					// next page load if it ever changes.
					quota_bytes: 100 * 1024 * 1024
				}
			];
		} catch (err) {
			createError = err instanceof Error ? err.message : t('error.createAccount');
		} finally {
			createBusy = false;
		}
	}

	async function copyCreatedPassword() {
		if (!createdUser) return;
		createdCopied = await copyText(createdUser.password);
	}

	function openDelete(user: AdminUser) {
		deleteUserTarget = user;
		deleteError = null;
	}

	function closeDelete() {
		deleteUserTarget = null;
	}

	async function submitDelete() {
		if (!deleteUserTarget) return;
		const target = deleteUserTarget;
		deleteError = null;
		deleteBusy = true;
		try {
			const { deleted_files } = await deleteUser(target.id);
			users = users.filter((u) => u.id !== target.id);
			successMessage = t('accounts.deleteDone', { name: target.nickname, n: deleted_files });
			errorMessage = null;
			closeDelete();
		} catch (err) {
			deleteError = err instanceof Error ? err.message : t('error.deleteAccount');
		} finally {
			deleteBusy = false;
		}
	}

	function startQuotaEdit(user: AdminUser) {
		quotaEditId = user.id;
		// Pre-filled with the same string the row displays, unit and all, so the
		// field and the label can never disagree. Rounding this into bare
		// megabytes turned any sub-megabyte quota into "0", and then merely
		// opening the editor and clicking away set the account's limit to zero.
		quotaInput = formatSize(user.quota_bytes);
	}

	function cancelQuotaEdit() {
		quotaEditId = null;
		quotaInput = '';
	}

	async function submitQuota(user: AdminUser) {
		// Enter fires this, then sets `disabled` on the still-focused input --
		// and the HTML spec has the browser blur an element that becomes
		// disabled, which fires onblur and would submit the same value twice.
		if (quotaBusy) return;

		const bytes = parseSize(quotaInput);
		if (bytes === null) {
			errorMessage = t('error.quotaInvalid');
			cancelQuotaEdit();
			return;
		}
		if (bytes === user.quota_bytes) {
			cancelQuotaEdit();
			return;
		}
		errorMessage = null;
		successMessage = null;
		quotaBusy = true;
		try {
			await setUserQuota(user.id, bytes);
			users = users.map((u) => (u.id === user.id ? { ...u, quota_bytes: bytes } : u));
		} catch (err) {
			errorMessage = err instanceof Error ? err.message : t('error.quota');
		} finally {
			quotaBusy = false;
			// Only close the editor if it is still this row's. Clicking straight
			// from one row's quota to another's blurs the first (starting this
			// call) before opening the second, so an unguarded close would shut
			// the editor the user just opened, tens of milliseconds later.
			if (quotaEditId === user.id) cancelQuotaEdit();
		}
	}
</script>

<svelte:window
	onkeydown={(e) => {
		if (e.key !== 'Escape') return;
		if (resetUser && !resetBusy) closeReset();
		else if (createOpen && !createBusy) closeCreate();
		else if (deleteUserTarget && !deleteBusy) closeDelete();
		else if (quotaEditId !== null && !quotaBusy) cancelQuotaEdit();
	}}
/>

<section class="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-6">
	<div class="flex flex-wrap items-start justify-between gap-3">
		<div>
			<h2 class="flex items-center gap-2 text-base font-semibold">
				<Icon icon="lucide:users" width="17" height="17" class="text-emerald-400" />
				{t('accounts.title')}
			</h2>
			<p class="mt-0.5 text-xs text-neutral-500">{t('accounts.subtitle')}</p>
		</div>
		<!-- Adding a colleague used to mean turning global self-registration on,
		     having them sign up, and turning it back off. -->
		<button
			onclick={openCreate}
			class="flex items-center gap-1.5 rounded-lg bg-neutral-800 px-3 py-1.5 text-xs font-medium text-neutral-200 transition-colors hover:bg-neutral-700"
		>
			<Icon icon="lucide:user-plus" width="14" height="14" />
			{t('accounts.create')}
		</button>
	</div>

	{#if errorMessage}
		<div
			role="alert"
			class="mt-4 flex items-center gap-2 rounded-lg border border-red-900/50 bg-red-500/10 px-3 py-2 text-sm text-red-400"
		>
			<Icon icon="lucide:alert-circle" width="16" height="16" class="shrink-0" />
			{errorMessage}
		</div>
	{/if}
	{#if successMessage}
		<div
			class="mt-4 flex items-center gap-2 rounded-lg border border-emerald-900/50 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-400"
		>
			<Icon icon="lucide:check-circle-2" width="16" height="16" class="shrink-0" />
			{successMessage}
		</div>
	{/if}

	<ul class="mt-5 overflow-hidden rounded-xl border border-neutral-800 bg-neutral-950/40">
		{#each users as user (user.id)}
			<li
				class="flex flex-wrap items-center gap-3 border-b border-neutral-800/60 px-3 py-3 last:border-0"
			>
				<span
					class={`flex h-8 w-8 shrink-0 items-center justify-center rounded-lg ${
						user.disabled ? 'bg-neutral-800 text-neutral-500' : 'bg-emerald-500/10 text-emerald-400'
					}`}
				>
					<Icon
						icon={user.is_super_admin ? 'lucide:shield-check' : 'lucide:user'}
						width="15"
						height="15"
					/>
				</span>

				<div class="min-w-0 flex-1">
					<div class="flex flex-wrap items-center gap-2">
						<span
							class={`truncate text-sm font-medium ${user.disabled ? 'text-neutral-500 line-through' : 'text-neutral-100'}`}
						>
							{user.nickname}
						</span>
						<span class="font-mono text-xs text-neutral-500">@{user.username}</span>
						{#if user.is_super_admin}
							<span
								class="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-medium tracking-wide text-emerald-400"
								title={t('accounts.superAdminLocked')}
							>
								{t('accounts.superAdmin')}
							</span>
						{/if}
						{#if user.disabled}
							<span
								class="rounded bg-red-500/10 px-1.5 py-0.5 text-[10px] font-medium tracking-wide text-red-400"
								title={t('accounts.disabledNote')}
							>
								{t('accounts.disabledBadge')}
							</span>
						{/if}
					</div>
					<div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-neutral-500">
						<span class="flex items-center gap-1">
							<Icon icon="lucide:file-code-2" width="12" height="12" />
							{t('accounts.files', { n: user.file_count })}
						</span>
						{#if user.trashed_count > 0}
							<span class="flex items-center gap-1">
								<Icon icon="lucide:trash-2" width="12" height="12" />
								{t('accounts.trashed', { n: user.trashed_count })}
							</span>
						{/if}
						{#if quotaEditId === user.id}
							<span class="flex items-center gap-1">
								<Icon icon="lucide:hard-drive" width="12" height="12" />
								<!-- svelte-ignore a11y_autofocus -->
								<input
									type="text"
									autofocus
									bind:value={quotaInput}
									disabled={quotaBusy}
									aria-label={t('accounts.quotaLabel')}
									placeholder={t('accounts.quotaUnitHint')}
									onkeydown={(e) => {
										if (e.key === 'Enter') submitQuota(user);
									}}
									onblur={() => submitQuota(user)}
									class="w-20 rounded border border-neutral-700 bg-neutral-950 px-1.5 py-0.5 font-mono text-xs text-neutral-100 outline-none focus:border-emerald-500"
								/>
								<button
									onmousedown={(e) => e.preventDefault()}
									onclick={cancelQuotaEdit}
									aria-label={t('common.cancel')}
									title={t('common.cancel')}
									class="rounded p-0.5 text-neutral-600 transition-colors hover:bg-neutral-800 hover:text-neutral-300"
								>
									<Icon icon="lucide:x" width="12" height="12" />
								</button>
							</span>
						{:else}
							<button
								onclick={() => startQuotaEdit(user)}
								title={t('accounts.quotaEdit')}
								class={`flex items-center gap-1 rounded px-1 transition-colors hover:bg-neutral-800 hover:text-neutral-300 ${
									user.used_bytes > user.quota_bytes ? 'text-red-400' : ''
								}`}
							>
								<Icon icon="lucide:hard-drive" width="12" height="12" />
								<span class="font-mono"
									>{formatSize(user.used_bytes)} / {formatSize(user.quota_bytes)}</span
								>
							</button>
						{/if}
						<span>{t('accounts.created', { date: formatDate(user.created_at) })}</span>
					</div>
				</div>

				<div class="flex shrink-0 items-center gap-1.5">
					<button
						onclick={() => openReset(user)}
						class="flex items-center gap-1.5 rounded-lg bg-neutral-800 px-2.5 py-1.5 text-xs font-medium text-neutral-200 transition-colors hover:bg-neutral-700"
					>
						<Icon icon="lucide:key-round" width="13" height="13" />
						{t('accounts.resetPassword')}
					</button>
					<!-- The super admin is the only account that can lift a suspension,
					     so it cannot suspend itself; the server refuses this too. -->
					<button
						onclick={() => toggleDisabled(user)}
						disabled={user.is_super_admin || busyId === user.id}
						title={user.is_super_admin
							? t('accounts.superAdminLocked')
							: user.disabled
								? t('accounts.enableTitle')
								: t('accounts.disableTitle')}
						class={`flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
							user.disabled
								? 'bg-neutral-800 text-emerald-400 hover:bg-neutral-700'
								: 'bg-neutral-800 text-neutral-200 hover:bg-red-500/10 hover:text-red-400'
						}`}
					>
						<Icon
							icon={user.disabled ? 'lucide:user-check' : 'lucide:user-x'}
							width="13"
							height="13"
						/>
						{user.disabled ? t('accounts.enable') : t('accounts.disable')}
					</button>
					<!-- Deleting the super admin would remove the only account that can
					     manage accounts, so it is refused here and on the server. -->
					<button
						onclick={() => openDelete(user)}
						disabled={user.is_super_admin}
						title={user.is_super_admin ? t('accounts.superAdminLocked') : t('accounts.deleteTitle')}
						aria-label={t('accounts.deleteTitle')}
						class="rounded-lg bg-neutral-800 p-1.5 text-neutral-400 transition-colors hover:bg-red-500/10 hover:text-red-400 disabled:cursor-not-allowed disabled:opacity-40"
					>
						<Icon icon="lucide:trash-2" width="13" height="13" />
					</button>
				</div>
			</li>
		{/each}
	</ul>
</section>

{#if resetUser}
	<div
		class="fixed inset-0 z-30 flex items-center justify-center bg-neutral-950/70 p-4 backdrop-blur-sm"
		onclick={() => !resetBusy && closeReset()}
		role="presentation"
	>
		<div
			class="flex w-full max-w-sm flex-col gap-4 rounded-2xl border border-neutral-800 bg-neutral-900 p-6 shadow-2xl"
			onclick={(e) => e.stopPropagation()}
			role="presentation"
		>
			<div class="flex items-center justify-between">
				<h2 class="text-lg font-semibold tracking-tight">{t('accounts.resetTitle')}</h2>
				<button
					onclick={closeReset}
					disabled={resetBusy}
					aria-label={t('common.close')}
					title={t('common.close')}
					class="rounded-lg p-1 text-neutral-500 transition-colors hover:bg-neutral-800 hover:text-neutral-200"
				>
					<Icon icon="lucide:x" width="18" height="18" />
				</button>
			</div>

			<p class="text-sm text-neutral-400">
				{t('accounts.resetBody', { name: resetUser.nickname })}
			</p>

			<label class="flex flex-col gap-1 text-xs font-medium text-neutral-400">
				{t('accounts.newPassword')}
				<input
					type="password"
					bind:value={resetPassword}
					autocomplete="new-password"
					onkeydown={(e) => {
						if (e.key === 'Enter') submitReset();
					}}
					class="rounded-lg border border-neutral-800 bg-neutral-950 px-3 py-2 text-sm text-neutral-100 outline-none focus:border-emerald-500"
				/>
			</label>

			{#if resetError}
				<p role="alert" class="text-sm text-red-400">{resetError}</p>
			{/if}

			<div class="flex justify-end gap-2">
				<button
					onclick={closeReset}
					disabled={resetBusy}
					class="rounded-lg px-4 py-2 text-sm font-medium text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100 disabled:opacity-50"
				>
					{t('common.cancel')}
				</button>
				<button
					onclick={submitReset}
					disabled={resetBusy}
					class="flex items-center gap-2 rounded-lg bg-emerald-500 px-4 py-2 text-sm font-medium text-neutral-950 transition-colors hover:bg-emerald-400 disabled:opacity-50"
				>
					{#if resetBusy}
						<Icon icon="lucide:loader-2" width="15" height="15" class="animate-spin" />
					{/if}
					{resetBusy ? t('common.saving') : t('accounts.resetPassword')}
				</button>
			</div>
		</div>
	</div>
{/if}

{#if createOpen}
	<div
		class="fixed inset-0 z-30 flex items-center justify-center bg-neutral-950/70 p-4 backdrop-blur-sm"
		onclick={() => !createBusy && closeCreate()}
		role="presentation"
	>
		<div
			class="flex max-h-[90vh] w-full max-w-sm flex-col gap-4 overflow-y-auto rounded-2xl border border-neutral-800 bg-neutral-900 p-6 shadow-2xl"
			onclick={(e) => e.stopPropagation()}
			role="presentation"
		>
			<div class="flex items-center justify-between">
				<h2 class="text-lg font-semibold tracking-tight">{t('accounts.createTitle')}</h2>
				<button
					onclick={closeCreate}
					disabled={createBusy}
					aria-label={t('common.close')}
					title={t('common.close')}
					class="rounded-lg p-1 text-neutral-500 transition-colors hover:bg-neutral-800 hover:text-neutral-200"
				>
					<Icon icon="lucide:x" width="18" height="18" />
				</button>
			</div>

			{#if createdUser}
				<!-- The generated password exists only in this response. Nothing
				     stores it in plaintext, so once this modal closes the only way
				     to give the account a known password is to reset it. -->
				<p class="text-sm text-neutral-400">
					{t('accounts.createdBody', { name: createdUser.nickname })}
				</p>
				<div class="flex flex-col gap-1.5 rounded-lg border border-neutral-800 bg-neutral-950 p-3">
					<span class="text-xs text-neutral-500">@{createdUser.username}</span>
					<div class="flex items-center gap-2">
						<code class="min-w-0 flex-1 truncate font-mono text-sm text-emerald-400"
							>{createdUser.password}</code
						>
						<button
							onclick={copyCreatedPassword}
							aria-label={t('accounts.copyPassword')}
							title={t('accounts.copyPassword')}
							class="shrink-0 rounded-lg bg-neutral-800 p-1.5 text-neutral-300 transition-colors hover:bg-neutral-700"
						>
							<Icon icon={createdCopied ? 'lucide:check' : 'lucide:copy'} width="14" height="14" />
						</button>
					</div>
				</div>
				<p class="text-xs text-amber-400/80">{t('accounts.createdWarning')}</p>
				<div class="flex justify-end">
					<button
						onclick={closeCreate}
						class="rounded-lg bg-emerald-500 px-4 py-2 text-sm font-medium text-neutral-950 transition-colors hover:bg-emerald-400"
					>
						{t('common.done')}
					</button>
				</div>
			{:else}
				<p class="text-sm text-neutral-400">{t('accounts.createBody')}</p>

				<label class="flex flex-col gap-1 text-xs font-medium text-neutral-400">
					{t('accounts.username')}
					<input
						type="text"
						bind:value={createUsername}
						autocomplete="off"
						class="rounded-lg border border-neutral-800 bg-neutral-950 px-3 py-2 font-mono text-sm text-neutral-100 outline-none focus:border-emerald-500"
					/>
				</label>
				<label class="flex flex-col gap-1 text-xs font-medium text-neutral-400">
					{t('accounts.nickname')}
					<input
						type="text"
						bind:value={createNickname}
						autocomplete="off"
						placeholder={createUsername}
						onkeydown={(e) => {
							if (e.key === 'Enter') submitCreate();
						}}
						class="rounded-lg border border-neutral-800 bg-neutral-950 px-3 py-2 text-sm text-neutral-100 outline-none focus:border-emerald-500"
					/>
				</label>

				{#if createError}
					<p role="alert" class="text-sm text-red-400">{createError}</p>
				{/if}

				<div class="flex justify-end gap-2">
					<button
						onclick={closeCreate}
						disabled={createBusy}
						class="rounded-lg px-4 py-2 text-sm font-medium text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100 disabled:opacity-50"
					>
						{t('common.cancel')}
					</button>
					<button
						onclick={submitCreate}
						disabled={createBusy}
						class="flex items-center gap-2 rounded-lg bg-emerald-500 px-4 py-2 text-sm font-medium text-neutral-950 transition-colors hover:bg-emerald-400 disabled:opacity-50"
					>
						{#if createBusy}
							<Icon icon="lucide:loader-2" width="15" height="15" class="animate-spin" />
						{/if}
						{createBusy ? t('common.saving') : t('accounts.create')}
					</button>
				</div>
			{/if}
		</div>
	</div>
{/if}

{#if deleteUserTarget}
	<div
		class="fixed inset-0 z-30 flex items-center justify-center bg-neutral-950/70 p-4 backdrop-blur-sm"
		onclick={() => !deleteBusy && closeDelete()}
		role="presentation"
	>
		<div
			class="flex max-h-[90vh] w-full max-w-sm flex-col gap-4 overflow-y-auto rounded-2xl border border-neutral-800 bg-neutral-900 p-6 shadow-2xl"
			onclick={(e) => e.stopPropagation()}
			role="presentation"
		>
			<div class="flex items-center justify-between">
				<h2 class="text-lg font-semibold tracking-tight text-red-400">
					{t('accounts.deleteTitle')}
				</h2>
				<button
					onclick={closeDelete}
					disabled={deleteBusy}
					aria-label={t('common.close')}
					title={t('common.close')}
					class="rounded-lg p-1 text-neutral-500 transition-colors hover:bg-neutral-800 hover:text-neutral-200"
				>
					<Icon icon="lucide:x" width="18" height="18" />
				</button>
			</div>

			<p class="text-sm text-neutral-400">
				{t('accounts.deleteBody', {
					name: deleteUserTarget.nickname,
					n: deleteUserTarget.file_count + deleteUserTarget.trashed_count
				})}
			</p>
			<p class="rounded-lg border border-red-900/50 bg-red-500/10 px-3 py-2 text-xs text-red-400">
				{t('accounts.deleteWarning')}
			</p>

			{#if deleteError}
				<p role="alert" class="text-sm text-red-400">{deleteError}</p>
			{/if}

			<div class="flex justify-end gap-2">
				<button
					onclick={closeDelete}
					disabled={deleteBusy}
					class="rounded-lg px-4 py-2 text-sm font-medium text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100 disabled:opacity-50"
				>
					{t('common.cancel')}
				</button>
				<button
					onclick={submitDelete}
					disabled={deleteBusy}
					class="flex items-center gap-2 rounded-lg bg-red-500 px-4 py-2 text-sm font-medium text-neutral-50 transition-colors hover:bg-red-400 disabled:opacity-50"
				>
					{#if deleteBusy}
						<Icon icon="lucide:loader-2" width="15" height="15" class="animate-spin" />
					{/if}
					{deleteBusy ? t('common.saving') : t('accounts.deleteConfirm')}
				</button>
			</div>
		</div>
	</div>
{/if}
