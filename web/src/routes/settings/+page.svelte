<script lang="ts">
	import { resolve } from '$app/paths';
	import Icon from '@iconify/svelte';
	import { t } from '$lib/i18n/index.svelte';
	import { copyText } from '$lib/clipboard';
	import LanguageSwitcher from '$lib/components/LanguageSwitcher.svelte';
	import Toggle from '$lib/components/Toggle.svelte';
	import AccountsSection from './AccountsSection.svelte';
	import BackupSection from './BackupSection.svelte';
	import { AuthApiError } from '$lib/api/auth';
	import { ensureApiKey, resetApiKey, updateProfile, updateSettings } from '$lib/api/settings';
	import { changePasswordSchema } from '$lib/schemas/register';
	import type { MessageKey } from '$lib/i18n/messages';

	let { data } = $props();

	const isAdmin = $derived(data.user?.is_admin ?? false);

	// --- User section ---
	// Intentional one-time snapshots (here and in the config section below):
	// these are edited locally / synced from API responses and shouldn't
	// re-sync if `data` changes.
	// svelte-ignore state_referenced_locally
	let nickname = $state(data.user?.nickname ?? '');
	let nicknameBusy = $state(false);
	let nicknameMessage = $state<string | null>(null);
	let nicknameError = $state<string | null>(null);

	let currentPassword = $state('');
	let newPassword = $state('');
	let rePassword = $state('');
	let passwordBusy = $state(false);
	let passwordMessage = $state<string | null>(null);
	let passwordError = $state<string | null>(null);

	// --- Config sections ---
	// svelte-ignore state_referenced_locally
	let allowRegistration = $state(data.settings.allow_registration);
	// svelte-ignore state_referenced_locally
	let mcpEnabled = $state(data.settings.mcp_enabled);
	// svelte-ignore state_referenced_locally
	let uploadDefaultPublic = $state(data.settings.upload_default_public);
	// svelte-ignore state_referenced_locally
	let apiKey = $state<string | null>(data.apiKey);
	let configError = $state<string | null>(null);
	let apiKeyCopied = $state(false);
	let copiedTimeout: ReturnType<typeof setTimeout> | undefined;

	// Ready-to-paste prompt that walks an AI agent through installing this MCP
	// server. Built through t(), so it re-renders in the active locale.
	const setupPrompt = $derived(
		apiKey ? t('settings.setupPromptText', { endpoint: `${location.origin}/mcp`, apiKey }) : ''
	);
	let promptCopied = $state(false);
	let promptCopiedTimeout: ReturnType<typeof setTimeout> | undefined;

	async function copySetupPrompt() {
		if (!setupPrompt) return;
		// copyText falls back to execCommand and reports failure rather than
		// throwing: navigator.clipboard does not exist over plain HTTP, which
		// is a supported way to run this app, and an unguarded call there made
		// the button do nothing at all with no error.
		promptCopied = await copyText(setupPrompt);
		if (!promptCopied) return;
		clearTimeout(promptCopiedTimeout);
		promptCopiedTimeout = setTimeout(() => (promptCopied = false), 1500);
	}

	// --- Reset confirmation modal ---
	let resetOpen = $state(false);
	let resetBusy = $state(false);

	async function saveNickname() {
		nicknameMessage = null;
		nicknameError = null;
		const trimmed = nickname.trim();
		if (!trimmed) {
			nicknameError = t('register.nicknameRequired');
			return;
		}
		nicknameBusy = true;
		try {
			await updateProfile({ nickname: trimmed });
			nicknameMessage = t('settings.nicknameUpdated');
		} catch {
			nicknameError = t('error.save');
		} finally {
			nicknameBusy = false;
		}
	}

	async function savePassword() {
		passwordMessage = null;
		passwordError = null;
		const parsed = changePasswordSchema.safeParse({
			currentPassword,
			password: newPassword,
			rePassword
		});
		if (!parsed.success) {
			passwordError = t(parsed.error.issues[0].message as MessageKey);
			return;
		}
		passwordBusy = true;
		try {
			await updateProfile({ current_password: currentPassword, new_password: newPassword });
			passwordMessage = t('settings.passwordUpdated');
			currentPassword = '';
			newPassword = '';
			rePassword = '';
		} catch (err) {
			passwordError =
				err instanceof AuthApiError && err.status === 403
					? t('error.wrongPassword')
					: t('error.save');
		} finally {
			passwordBusy = false;
		}
	}

	async function toggleRegistration() {
		configError = null;
		try {
			const next = await updateSettings({ allow_registration: !allowRegistration });
			allowRegistration = next.allow_registration;
		} catch {
			configError = t('error.updateSettings');
		}
	}

	async function toggleUploadDefaultPublic() {
		configError = null;
		try {
			const next = await updateSettings({ upload_default_public: !uploadDefaultPublic });
			uploadDefaultPublic = next.upload_default_public;
		} catch {
			configError = t('error.updateSettings');
		}
	}

	async function toggleMcp() {
		configError = null;
		try {
			const next = await updateSettings({ mcp_enabled: !mcpEnabled });
			mcpEnabled = next.mcp_enabled;
			if (mcpEnabled && !apiKey) {
				// Enabling MCP issues the user an API key (reused if one exists).
				apiKey = await ensureApiKey();
			}
		} catch (err) {
			configError =
				err instanceof AuthApiError && mcpEnabled ? t('error.apiKey') : t('error.updateSettings');
		}
	}

	async function copyApiKey() {
		if (!apiKey) return;
		apiKeyCopied = await copyText(apiKey);
		if (!apiKeyCopied) return;
		clearTimeout(copiedTimeout);
		copiedTimeout = setTimeout(() => (apiKeyCopied = false), 1500);
	}

	async function confirmReset() {
		configError = null;
		resetBusy = true;
		try {
			apiKey = await resetApiKey();
			resetOpen = false;
		} catch {
			configError = t('error.apiKey');
		} finally {
			resetBusy = false;
		}
	}
</script>

<svelte:window
	onkeydown={(e) => {
		if (e.key === 'Escape' && resetOpen && !resetBusy) resetOpen = false;
	}}
/>

<div class="min-h-screen bg-neutral-950 text-neutral-100">
	<header
		class="sticky top-0 z-10 border-b border-neutral-800 bg-neutral-950/80 px-6 py-4 backdrop-blur sm:px-10"
	>
		<div class="mx-auto flex max-w-3xl items-center justify-between">
			<div class="flex items-center gap-3">
				<a
					href={resolve('/')}
					title={t('settings.back')}
					class="flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100"
				>
					<Icon icon="lucide:arrow-left" width="15" height="15" />
					{t('settings.back')}
				</a>
			</div>
			<div class="flex items-center gap-4">
				<LanguageSwitcher />
			</div>
		</div>
	</header>

	<main class="mx-auto flex max-w-3xl flex-col gap-8 px-6 py-10 sm:px-10">
		<h1 class="text-xl font-semibold tracking-tight">{t('settings.title')}</h1>

		<!-- Section: user -->
		<section class="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-6">
			<h2 class="flex items-center gap-2 text-base font-semibold">
				<Icon icon="lucide:user" width="17" height="17" class="text-emerald-400" />
				{t('settings.user')}
			</h2>

			<div class="mt-5 flex flex-col gap-6">
				<div class="flex flex-col gap-1.5">
					<label
						for="nickname"
						class="text-xs font-medium tracking-wide text-neutral-400 uppercase"
					>
						{t('settings.nickname')}
					</label>
					<div class="flex gap-2">
						<input
							id="nickname"
							bind:value={nickname}
							class="w-full max-w-xs rounded-lg border border-neutral-700 bg-neutral-800/60 px-3 py-2 text-sm text-neutral-100 outline-none transition-colors focus:border-emerald-500"
						/>
						<button
							onclick={saveNickname}
							disabled={nicknameBusy}
							class="rounded-lg bg-neutral-800 px-4 py-2 text-sm font-medium text-neutral-200 transition-colors hover:bg-neutral-700 disabled:opacity-50"
						>
							{nicknameBusy ? t('common.saving') : t('common.save')}
						</button>
					</div>
					{#if nicknameMessage}
						<p class="text-xs text-emerald-400">{nicknameMessage}</p>
					{/if}
					{#if nicknameError}
						<p class="text-xs text-red-400">{nicknameError}</p>
					{/if}
				</div>

				<div class="flex flex-col gap-1.5">
					<p class="text-xs font-medium tracking-wide text-neutral-400 uppercase">
						{t('settings.changePassword')}
					</p>
					<div class="flex max-w-xs flex-col gap-2">
						<input
							type="password"
							bind:value={currentPassword}
							placeholder={t('settings.currentPassword')}
							autocomplete="current-password"
							class="rounded-lg border border-neutral-700 bg-neutral-800/60 px-3 py-2 text-sm text-neutral-100 placeholder-neutral-500 outline-none transition-colors focus:border-emerald-500"
						/>
						<input
							type="password"
							bind:value={newPassword}
							placeholder={t('settings.newPassword')}
							autocomplete="new-password"
							class="rounded-lg border border-neutral-700 bg-neutral-800/60 px-3 py-2 text-sm text-neutral-100 placeholder-neutral-500 outline-none transition-colors focus:border-emerald-500"
						/>
						<input
							type="password"
							bind:value={rePassword}
							placeholder={t('register.rePassword')}
							autocomplete="new-password"
							class="rounded-lg border border-neutral-700 bg-neutral-800/60 px-3 py-2 text-sm text-neutral-100 placeholder-neutral-500 outline-none transition-colors focus:border-emerald-500"
						/>
						<button
							onclick={savePassword}
							disabled={passwordBusy}
							class="self-start rounded-lg bg-neutral-800 px-4 py-2 text-sm font-medium text-neutral-200 transition-colors hover:bg-neutral-700 disabled:opacity-50"
						>
							{passwordBusy ? t('common.saving') : t('common.save')}
						</button>
					</div>
					{#if passwordMessage}
						<p class="text-xs text-emerald-400">{passwordMessage}</p>
					{/if}
					{#if passwordError}
						<p class="text-xs text-red-400">{passwordError}</p>
					{/if}
				</div>
			</div>
		</section>

		<!-- Sections: accounts and backup/restore. Both are rendered only for the
		     super admin, unlike the config toggles below, which everyone sees
		     (disabled) since knowing the instance's policy is useful. `users` is
		     null for anyone else, so this can't render a list it doesn't have. -->
		{#if isAdmin && data.users}
			<AccountsSection users={data.users} />
		{/if}
		{#if isAdmin}
			<BackupSection />
		{/if}

		<!-- Section: registration -->
		<section class="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-6">
			<h2 class="flex items-center gap-2 text-base font-semibold">
				<Icon icon="lucide:user-plus" width="17" height="17" class="text-emerald-400" />
				{t('settings.registration')}
			</h2>

			<div class="mt-5 flex items-start justify-between gap-4">
				<div>
					<p class="text-sm text-neutral-200">{t('config.allowRegistration')}</p>
					<p class="mt-0.5 text-xs text-neutral-500">
						{t('config.allowRegistrationHint')}
						{#if !isAdmin}
							{t('settings.adminOnly')}
						{/if}
					</p>
				</div>
				<Toggle
					checked={allowRegistration}
					disabled={!isAdmin}
					label={t('config.allowRegistration')}
					onToggle={toggleRegistration}
				/>
			</div>
		</section>

		<!-- Section: uploads -->
		<section class="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-6">
			<h2 class="flex items-center gap-2 text-base font-semibold">
				<Icon icon="lucide:upload" width="17" height="17" class="text-emerald-400" />
				{t('settings.uploads')}
			</h2>

			<div class="mt-5 flex items-start justify-between gap-4">
				<div>
					<p class="text-sm text-neutral-200">{t('config.uploadDefaultPublic')}</p>
					<p class="mt-0.5 text-xs text-neutral-500">
						{t('config.uploadDefaultPublicHint')}
						{#if !isAdmin}
							{t('settings.adminOnly')}
						{/if}
					</p>
				</div>
				<Toggle
					checked={uploadDefaultPublic}
					disabled={!isAdmin}
					label={t('config.uploadDefaultPublic')}
					onToggle={toggleUploadDefaultPublic}
				/>
			</div>
		</section>

		<!-- Section: AI capability -->
		<section class="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-6">
			<h2 class="flex items-center gap-2 text-base font-semibold">
				<Icon icon="lucide:sparkles" width="17" height="17" class="text-emerald-400" />
				{t('settings.ai')}
			</h2>

			<div class="mt-5 flex items-start justify-between gap-4">
				<div>
					<p class="text-sm text-neutral-200">{t('config.enableMcp')}</p>
					<p class="mt-0.5 text-xs text-neutral-500">
						{t('config.enableMcpHint')}
						{#if !isAdmin}
							{t('settings.adminOnly')}
						{/if}
					</p>
				</div>
				<Toggle
					checked={mcpEnabled}
					disabled={!isAdmin}
					label={t('config.enableMcp')}
					onToggle={toggleMcp}
				/>
			</div>

			{#if mcpEnabled && apiKey}
				<div class="mt-5 border-t border-neutral-800 pt-5">
					<p class="text-xs font-medium tracking-wide text-neutral-400 uppercase">
						{t('settings.apiKey')}
					</p>
					<p class="mt-0.5 text-xs text-neutral-500">
						{t('settings.apiKeyHint', { endpoint: `${location.origin}/mcp` })}
					</p>
					<div class="mt-2 flex flex-wrap items-center gap-2">
						<code
							class="rounded-lg border border-neutral-800 bg-neutral-950 px-3 py-2 font-mono text-xs text-neutral-300"
						>
							{apiKey}
						</code>
						<button
							onclick={copyApiKey}
							title={t('settings.copy')}
							class="flex items-center gap-1.5 rounded-lg px-2.5 py-2 text-sm text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100"
						>
							<Icon icon={apiKeyCopied ? 'lucide:check' : 'lucide:copy'} width="15" height="15" />
							{apiKeyCopied ? t('settings.copied') : t('settings.copy')}
						</button>
						<button
							onclick={() => (resetOpen = true)}
							class="flex items-center gap-1.5 rounded-lg px-2.5 py-2 text-sm text-red-400 transition-colors hover:bg-red-500/10"
						>
							<Icon icon="lucide:rotate-ccw" width="15" height="15" />
							{t('settings.resetApiKey')}
						</button>
					</div>
				</div>

				<div class="mt-5 border-t border-neutral-800 pt-5">
					<div class="flex items-start justify-between gap-4">
						<div>
							<p class="text-xs font-medium tracking-wide text-neutral-400 uppercase">
								{t('settings.setupPrompt')}
							</p>
							<p class="mt-0.5 text-xs text-neutral-500">{t('settings.setupPromptHint')}</p>
						</div>
						<button
							onclick={copySetupPrompt}
							title={t('settings.copy')}
							class="flex shrink-0 items-center gap-1.5 rounded-lg px-2.5 py-2 text-sm text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100"
						>
							<Icon icon={promptCopied ? 'lucide:check' : 'lucide:copy'} width="15" height="15" />
							{promptCopied ? t('settings.copied') : t('settings.copy')}
						</button>
					</div>
					<pre
						class="mt-2 rounded-lg border border-neutral-800 bg-neutral-950 p-3 text-xs leading-relaxed break-words whitespace-pre-wrap text-neutral-300">{setupPrompt}</pre>
				</div>
			{/if}

			{#if configError}
				<div
					role="alert"
					class="mt-4 flex items-center gap-2 rounded-lg border border-red-900/50 bg-red-500/10 px-3 py-2 text-sm text-red-400"
				>
					<Icon icon="lucide:alert-circle" width="16" height="16" class="shrink-0" />
					{configError}
				</div>
			{/if}
		</section>
	</main>
</div>

{#if resetOpen}
	<div
		class="fixed inset-0 z-30 flex items-center justify-center bg-neutral-950/70 p-4 backdrop-blur-sm"
		onclick={() => !resetBusy && (resetOpen = false)}
		role="presentation"
	>
		<div
			class="flex w-full max-w-sm flex-col gap-4 rounded-2xl border border-neutral-800 bg-neutral-900 p-6 shadow-2xl"
			onclick={(e) => e.stopPropagation()}
			role="presentation"
		>
			<div class="flex items-center justify-between">
				<h2 class="text-lg font-semibold tracking-tight">{t('settings.resetConfirmTitle')}</h2>
				<button
					onclick={() => (resetOpen = false)}
					disabled={resetBusy}
					class="rounded-lg p-1 text-neutral-500 transition-colors hover:bg-neutral-800 hover:text-neutral-200"
				>
					<Icon icon="lucide:x" width="18" height="18" />
				</button>
			</div>

			<p class="text-sm text-neutral-400">{t('settings.resetConfirmBody')}</p>

			<div class="flex justify-end gap-2">
				<button
					onclick={() => (resetOpen = false)}
					disabled={resetBusy}
					class="rounded-lg px-4 py-2 text-sm text-neutral-400 transition-colors hover:bg-neutral-800 hover:text-neutral-100 disabled:opacity-50"
				>
					{t('common.cancel')}
				</button>
				<button
					onclick={confirmReset}
					disabled={resetBusy}
					class="flex items-center gap-2 rounded-lg bg-red-500/90 px-4 py-2 text-sm font-medium text-neutral-950 transition-colors hover:bg-red-400 disabled:opacity-50"
				>
					<Icon icon="lucide:rotate-ccw" width="15" height="15" />
					{resetBusy ? t('settings.resetting') : t('settings.resetConfirm')}
				</button>
			</div>
		</div>
	</div>
{/if}
