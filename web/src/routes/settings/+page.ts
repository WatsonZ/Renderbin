import { ensureApiKey, getSettings } from '$lib/api/settings';

export async function load({ fetch }) {
	const settings = await getSettings(fetch);
	// MCP already enabled → surface the key immediately (created lazily on
	// first visit, reused afterwards).
	let apiKey: string | null = null;
	if (settings.mcp_enabled) {
		try {
			apiKey = await ensureApiKey(fetch);
		} catch {
			apiKey = null;
		}
	}
	return { settings, apiKey };
}
