import { AuthApiError } from './auth';

export interface Settings {
	allow_registration: boolean;
	mcp_enabled: boolean;
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
