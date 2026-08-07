<script lang="ts">
	import Icon from '@iconify/svelte';
	import { getLocale, setLocale, t } from '$lib/i18n/index.svelte';
	import type { Locale } from '$lib/i18n/messages';

	const options: { key: Locale; label: string }[] = [
		{ key: 'zh', label: '中' },
		{ key: 'en', label: 'EN' }
	];
</script>

<div
	class="flex items-center gap-0.5 rounded-lg px-1 py-1 text-sm text-neutral-400"
	title={t('switcher.label')}
>
	<Icon icon="lucide:languages" width="15" height="15" class="mr-1 text-neutral-500" />
	{#each options as opt (opt.key)}
		<button
			onclick={() => setLocale(opt.key)}
			aria-pressed={getLocale() === opt.key}
			class={`rounded-md px-2 py-0.5 text-xs font-medium transition-colors ${
				getLocale() === opt.key
					? 'bg-neutral-800 text-neutral-100'
					: 'text-neutral-500 hover:text-neutral-200'
			}`}
		>
			{opt.label}
		</button>
	{/each}
</div>
