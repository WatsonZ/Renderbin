import { ApiError } from './files';

/**
 * Account management, super admin only — every endpoint here 403s for anyone
 * else. Note what is deliberately absent: there is no way to read another
 * account's files, so the counts are the most this surface exposes of them.
 */
export interface AdminUser {
	id: number;
	username: string;
	nickname: string;
	is_super_admin: boolean;
	disabled: boolean;
	disabled_at: string | null;
	created_at: string;
	file_count: number;
	trashed_count: number;
	/** Bytes stored by this account, trashed files included — the same figure
	 *  the upload check enforces against quota_bytes. */
	used_bytes: number;
	quota_bytes: number;
}

/** The one and only time an admin-created account's password is readable. */
export interface CreatedUser {
	id: number;
	username: string;
	nickname: string;
	password: string;
}

async function unwrap<T>(res: Response): Promise<T> {
	if (!res.ok) {
		throw new ApiError(res.status, (await res.text()).trim());
	}
	return res.json();
}

async function expectNoContent(res: Response): Promise<void> {
	if (!res.ok) {
		throw new ApiError(res.status, (await res.text()).trim());
	}
}

/** `fetch` is a parameter so SvelteKit `load` functions can pass their own. */
export function listUsers(f: typeof fetch = fetch): Promise<AdminUser[]> {
	return f('/api/admin/users').then((res) => unwrap<AdminUser[]>(res));
}

/**
 * Suspend or restore an account. Suspending blocks sign-in, invalidates the
 * sessions and MCP key it already has, and makes every link to its files 404.
 * Returns nothing: the caller flips its own row after a successful call, since
 * the server's row alone couldn't carry the file counts the list shows.
 */
export function setUserDisabled(id: number, disabled: boolean): Promise<void> {
	return fetch(`/api/admin/users/${id}/status`, {
		method: 'PATCH',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ disabled })
	}).then(expectNoContent);
}

/** Set an account's password without knowing the old one, and sign it out everywhere. */
export function resetUserPassword(id: number, newPassword: string): Promise<void> {
	return fetch(`/api/admin/users/${id}/password`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ new_password: newPassword })
	}).then(expectNoContent);
}

/**
 * Create an account. The server generates the password and returns it once —
 * nothing stores it in plaintext, so the caller has to show it before the
 * response is discarded.
 */
export function createUser(username: string, nickname: string): Promise<CreatedUser> {
	return fetch('/api/admin/users', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ username, nickname })
	}).then((res) => unwrap<CreatedUser>(res));
}

/**
 * Delete an account and hard-delete every file it owns, in one transaction.
 * Irreversible and not routed through the trash — suspending is the reversible
 * option. The server refuses the super admin and the caller's own account.
 */
export function deleteUser(id: number): Promise<{ deleted_files: number }> {
	return fetch(`/api/admin/users/${id}`, { method: 'DELETE' }).then((res) =>
		unwrap<{ deleted_files: number }>(res)
	);
}

/**
 * Change how much an account may store. Lowering it below current usage blocks
 * further uploads and leaves stored files alone.
 */
export function setUserQuota(id: number, quotaBytes: number): Promise<void> {
	return fetch(`/api/admin/users/${id}/quota`, {
		method: 'PATCH',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ quota_bytes: quotaBytes })
	}).then(expectNoContent);
}
