<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import Icon from '@iconify/svelte';
	import { superForm, defaults } from 'sveltekit-superforms';
	import { zod4 as zod, zod4Client as zodClient } from 'sveltekit-superforms/adapters';
	import { loginSchema } from '$lib/schemas/login';
	import { AuthApiError, login } from '$lib/api/auth';
	import { t } from '$lib/i18n/index.svelte';
	import type { MessageKey } from '$lib/i18n/messages';
	import LanguageSwitcher from '$lib/components/LanguageSwitcher.svelte';

	let { data } = $props();

	let errorMessage = $state<string | null>(null);

	const { form, errors, enhance, submitting } = superForm(defaults(zod(loginSchema)), {
		SPA: true,
		validators: zodClient(loginSchema),
		async onUpdate({ form }) {
			if (!form.valid) return;
			errorMessage = null;
			try {
				await login(form.data.username, form.data.password);
				await goto(resolve('/'));
			} catch (err) {
				// 403 means the credentials were correct but the account is
				// suspended; showing "wrong password" there would have someone
				// retyping a password that works.
				errorMessage =
					err instanceof AuthApiError && err.status === 403
						? t('login.accountDisabled')
						: t('login.invalidCredentials');
			}
		}
	});
</script>

<main class="relative flex min-h-screen items-center justify-center bg-neutral-950 p-6">
	<div class="absolute top-4 right-4">
		<LanguageSwitcher />
	</div>
	<div class="w-full max-w-sm">
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
			<h1 class="text-lg font-semibold text-neutral-100">{t('login.signIn')}</h1>
			<p class="mt-1 mb-6 text-sm text-neutral-500">{t('login.subtitle')}</p>

			<form method="POST" use:enhance class="flex flex-col gap-4">
				<label class="flex flex-col gap-1.5">
					<span class="text-xs font-medium tracking-wide text-neutral-400 uppercase">
						{t('login.username')}
					</span>
					<input
						name="username"
						bind:value={$form.username}
						autocomplete="username"
						class="rounded-lg border border-neutral-700 bg-neutral-800/60 px-3 py-2.5 text-sm text-neutral-100
							placeholder-neutral-500 outline-none transition-colors focus:border-emerald-500"
					/>
					{#if $errors.username}
						<span class="text-xs text-red-400">{t($errors.username[0] as MessageKey)}</span>
					{/if}
				</label>

				<label class="flex flex-col gap-1.5">
					<span class="text-xs font-medium tracking-wide text-neutral-400 uppercase">
						{t('login.password')}
					</span>
					<input
						type="password"
						name="password"
						bind:value={$form.password}
						autocomplete="current-password"
						class="rounded-lg border border-neutral-700 bg-neutral-800/60 px-3 py-2.5 text-sm text-neutral-100
							placeholder-neutral-500 outline-none transition-colors focus:border-emerald-500"
					/>
					{#if $errors.password}
						<span class="text-xs text-red-400">{t($errors.password[0] as MessageKey)}</span>
					{/if}
				</label>

				<button
					type="submit"
					disabled={$submitting}
					class="mt-2 flex items-center justify-center gap-2 rounded-lg bg-emerald-500 py-2.5 text-sm
						font-medium text-neutral-950 transition-colors hover:bg-emerald-400 disabled:opacity-50"
				>
					<Icon icon="lucide:log-in" width="16" height="16" />
					{$submitting ? t('login.signingIn') : t('login.signIn')}
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

			{#if data.allowRegistration}
				<p class="mt-6 text-center text-sm text-neutral-500">
					{t('login.noAccount')}
					<a
						href={resolve('/register')}
						class="text-emerald-400 transition-colors hover:text-emerald-300"
					>
						{t('login.register')}
					</a>
				</p>
			{/if}
		</div>
	</div>
</main>
