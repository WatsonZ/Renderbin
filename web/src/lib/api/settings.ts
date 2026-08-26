import { AuthApiError } from './auth';

export interface Settings {
	allow_registration: boolean;
	mcp_enabled: boolean;
	upload_default_public: boolean;
}

export async function getSettings(fetchFn: typeof fetch = fetch): Promise<Settings> {
	const res = await fetchFn('/api/settings');
	if (!res.ok) {
		throw new AuthApiError(res.status, 'Failed to load settings');
	}
	return res.json();
}

/** Super-admin only; fields are optional so a single toggle can be sent alone. */
export async function updateSettings(patch: Partial<Settings>): Promise<Settings> {
	const res = await fetch('/api/settings', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(patch)
	});
	if (!res.ok) {
		throw new AuthApiError(res.status, await res.text());
	}
	return res.json();
}

export interface Usage {
	used_bytes: number;
	quota_bytes: number;
}

/**
 * The caller's storage use and limit. Its own endpoint rather than a field on
 * `/api/auth/me` because the layout guard hits that on every navigation and
 * this one runs an aggregate. Counts trashed files, matching what the server
 * enforces on upload, so the figure shown is the figure that will be applied.
 */
export function getUsage(fetchFn: typeof fetch = fetch): Promise<Usage> {
	return fetchFn('/api/user/usage').then(async (res) => {
		if (!res.ok) {
			throw new AuthApiError(res.status, (await res.text()).trim());
		}
		return res.json();
	});
}

export interface ProfilePatch {
	nickname?: string;
	current_password?: string;
	new_password?: string;
}

export async function updateProfile(patch: ProfilePatch): Promise<void> {
	const res = await fetch('/api/user', {
		method: 'PATCH',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(patch)
	});
	if (!res.ok) {
		throw new AuthApiError(res.status, await res.text());
	}
}

async function apiKeyRequest(path: string, fetchFn: typeof fetch): Promise<string> {
	const res = await fetchFn(path, { method: 'POST' });
	if (!res.ok) {
		throw new AuthApiError(res.status, await res.text());
	}
	const body: { api_key: string } = await res.json();
	return body.api_key;
}

/** Returns the current user's MCP API key, creating one on first call. */
export function ensureApiKey(fetchFn: typeof fetch = fetch): Promise<string> {
	return apiKeyRequest('/api/user/api-key', fetchFn);
}

/** Replaces the current user's MCP API key with a fresh one. */
export function resetApiKey(fetchFn: typeof fetch = fetch): Promise<string> {
	return apiKeyRequest('/api/user/api-key/reset', fetchFn);
}

export interface RestoreResult {
	users: number;
	files: number;
}

/**
 * Replaces the whole database with an uploaded snapshot. Super admin only.
 *
 * The body is the raw `.db` file — there is exactly one field, so a multipart
 * wrapper would only add parsing on both ends. A rejected upload changes
 * nothing, but a *successful* one replaces the sessions table too, so the caller
 * should expect its own session to be gone and reload.
 */
export async function restoreBackup(file: File): Promise<RestoreResult> {
	const res = await fetch('/api/backup/restore', {
		method: 'POST',
		headers: { 'Content-Type': 'application/octet-stream' },
		body: file
	});
	if (!res.ok) {
		throw new AuthApiError(res.status, (await res.text()).trim());
	}
	return res.json();
}
