<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import Icon from '@iconify/svelte';
	import { superForm, defaults } from 'sveltekit-superforms';
	import { zod4 as zod, zod4Client as zodClient } from 'sveltekit-superforms/adapters';
	import { registerSchema } from '$lib/schemas/register';
	import { setup } from '$lib/api/auth';
	import { t } from '$lib/i18n/index.svelte';
	import type { MessageKey } from '$lib/i18n/messages';
	import LanguageSwitcher from '$lib/components/LanguageSwitcher.svelte';
	import Toggle from '$lib/components/Toggle.svelte';

	let errorMessage = $state<string | null>(null);
	// Initial configs chosen during setup; plain toggles, not form fields.
	let allowRegistration = $state(false);
	let mcpEnabled = $state(false);

	const { form, errors, enhance, submitting } = superForm(defaults(zod(registerSchema)), {
		SPA: true,
		validators: zodClient(registerSchema),
		async onUpdate({ form }) {
			if (!form.valid) return;
			errorMessage = null;
			try {
				await setup({
					username: form.data.username,
					nickname: form.data.nickname,
					password: form.data.password,
					allow_registration: allowRegistration,
					mcp_enabled: mcpEnabled
				});
				await goto(resolve('/'));
			} catch {
				errorMessage = t('welcome.failed');
			}
		}
	});

	const fieldClass =
		'rounded-lg border border-neutral-700 bg-neutral-800/60 px-3 py-2.5 text-sm text-neutral-100 ' +
		'placeholder-neutral-500 outline-none transition-colors focus:border-emerald-500';
	const labelClass = 'text-xs font-medium tracking-wide text-neutral-400 uppercase';
</script>

<main class="relative flex min-h-screen items-center justify-center bg-neutral-950 p-6">
	<div class="absolute top-4 right-4">
		<LanguageSwitcher />
	</div>
	<div class="w-full max-w-md">
		<div class="mb-8 flex items-center justify-center gap-2.5">
			<span
				class="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-500 text-neutral-950"
			>
				<Icon icon="lucide:file-code-2" width="20" height="20" />
			</span>
			<span class="text-lg font-semibold tracking-tight text-neutral-100">Renderbin</span>
		</div>

		<div
			class="rounded-2xl border border-neutral-800 bg-neutral-900/60 p-8 shadow-2xl shadow-black/40"
		>
			<h1 class="text-lg font-semibold text-neutral-100">{t('welcome.title')}</h1>
			<p class="mt-1 mb-6 text-sm text-neutral-500">{t('welcome.subtitle')}</p>

			<form method="POST" use:enhance class="flex flex-col gap-4">
				<label class="flex flex-col gap-1.5">
					<span class={labelClass}>{t('login.username')}</span>
					<input
						name="username"
						bind:value={$form.username}
						autocomplete="username"
						class={fieldClass}
					/>
					{#if $errors.username}
						<span class="text-xs text-red-400">{t($errors.username[0] as MessageKey)}</span>
					{/if}
				</label>

				<label class="flex flex-col gap-1.5">
					<span class={labelClass}>{t('register.nickname')}</span>
					<input
						name="nickname"
						bind:value={$form.nickname}
						autocomplete="nickname"
						class={fieldClass}
					/>
					{#if $errors.nickname}
						<span class="text-xs text-red-400">{t($errors.nickname[0] as MessageKey)}</span>
					{/if}
				</label>

				<label class="flex flex-col gap-1.5">
					<span class={labelClass}>{t('login.password')}</span>
					<input
						type="password"
						name="password"
						bind:value={$form.password}
						autocomplete="new-password"
						class={fieldClass}
					/>
					{#if $errors.password}
						<span class="text-xs text-red-400">{t($errors.password[0] as MessageKey)}</span>
					{/if}
				</label>

				<label class="flex flex-col gap-1.5">
					<span class={labelClass}>{t('register.rePassword')}</span>
					<input
						type="password"
						name="rePassword"
						bind:value={$form.rePassword}
						autocomplete="new-password"
						class={fieldClass}
					/>
					{#if $errors.rePassword}
						<span class="text-xs text-red-400">{t($errors.rePassword[0] as MessageKey)}</span>
					{/if}
				</label>

				<div
					class="mt-2 flex flex-col gap-4 rounded-lg border border-neutral-800 bg-neutral-950/50 p-4"
				>
					<p class={labelClass}>{t('welcome.options')}</p>

					<div class="flex items-start justify-between gap-4">
						<div>
							<p class="text-sm text-neutral-200">{t('config.allowRegistration')}</p>
							<p class="mt-0.5 text-xs text-neutral-500">{t('config.allowRegistrationHint')}</p>
						</div>
						<Toggle
							checked={allowRegistration}
							label={t('config.allowRegistration')}
							onToggle={() => (allowRegistration = !allowRegistration)}
						/>
					</div>

					<div class="flex items-start justify-between gap-4">
						<div>
							<p class="text-sm text-neutral-200">{t('config.enableMcp')}</p>
							<p class="mt-0.5 text-xs text-neutral-500">{t('config.enableMcpHint')}</p>
						</div>
						<Toggle
							checked={mcpEnabled}
							label={t('config.enableMcp')}
							onToggle={() => (mcpEnabled = !mcpEnabled)}
						/>
					</div>
				</div>

				<button
					type="submit"
					disabled={$submitting}
					class="mt-2 flex items-center justify-center gap-2 rounded-lg bg-emerald-500 py-2.5 text-sm
						font-medium text-neutral-950 transition-colors hover:bg-emerald-400 disabled:opacity-50"
				>
					<Icon icon="lucide:rocket" width="16" height="16" />
					{$submitting ? t('welcome.submitting') : t('welcome.submit')}
				</button>
			</form>

			{#if errorMessage}
				<div
					class="mt-4 flex items-center gap-2 rounded-lg border border-red-900/50 bg-red-500/10 px-3 py-2 text-sm text-red-400"
				>
					<Icon icon="lucide:alert-circle" width="16" height="16" class="shrink-0" />
					{errorMessage}
				</div>
			{/if}
		</div>
	</div>
</main>
