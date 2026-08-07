import { redirect } from '@sveltejs/kit';
import type { Me, SetupStatus } from '$lib/api/auth';

export const ssr = false;

// Auth guard for the whole SPA. While the users table is empty every route
// funnels into /welcome (first-run setup); afterwards /login and /register
// stay public and everything else requires a session.
export async function load({ url, fetch }) {
	let status: SetupStatus = { needs_setup: false, allow_registration: false };
	const statusRes = await fetch('/api/setup/status');
	if (statusRes.ok) {
		status = await statusRes.json();
	}

	if (status.needs_setup) {
		if (url.pathname !== '/welcome') redirect(302, '/welcome');
		return { allowRegistration: status.allow_registration, user: null };
	}
	if (url.pathname === '/welcome') redirect(302, '/login');

	if (url.pathname === '/login' || url.pathname === '/register') {
		return { allowRegistration: status.allow_registration, user: null };
	}

	const res = await fetch('/api/auth/me');
	if (!res.ok) {
		redirect(302, '/login');
	}
	const user: Me = await res.json();
	return { allowRegistration: status.allow_registration, user };
}
