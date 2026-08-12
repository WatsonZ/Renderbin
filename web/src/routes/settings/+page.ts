import { ensureApiKey, getSettings } from '$lib/api/settings';
import { listUsers, type AdminUser } from '$lib/api/admin';

export async function load({ fetch, parent }) {
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

	// The account list and the backup controls are super-admin-only sections of
	// this page. The server is the authority on that (every /api/admin handler
	// checks it itself), so a failure here just means "don't render the section"
	// rather than an error the page has to explain.
	const { user } = await parent();
	let users: AdminUser[] | null = null;
	if (user?.is_admin) {
		try {
			users = await listUsers(fetch);
		} catch {
			users = null;
		}
	}

	return { settings, apiKey, users };
}
