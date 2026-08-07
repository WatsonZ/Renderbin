<script lang="ts">
	// Simple on/off switch. Controlled: the parent owns the value and flips it
	// in onToggle (usually after an API call), so a failed save never leaves
	// the knob out of sync with the server.
	let {
		checked,
		disabled = false,
		label,
		onToggle
	}: {
		checked: boolean;
		disabled?: boolean;
		label: string;
		onToggle: () => void;
	} = $props();
</script>

<button
	type="button"
	role="switch"
	aria-checked={checked}
	aria-label={label}
	{disabled}
	onclick={onToggle}
	class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors
		disabled:cursor-not-allowed disabled:opacity-40
		{checked ? 'bg-emerald-500' : 'bg-neutral-700'}"
>
	<span
		class="inline-block h-4.5 w-4.5 transform rounded-full bg-neutral-100 transition-transform
			{checked ? 'translate-x-5.5' : 'translate-x-1'}"
	></span>
</button>
