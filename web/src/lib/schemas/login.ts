import { z } from 'zod';

// Validation messages are i18n message keys, not display text — the login page
// resolves them through t() so errors render in the active locale.
export const loginSchema = z.object({
	username: z.string().trim().min(1, 'login.usernameRequired'),
	password: z.string().min(1, 'login.passwordRequired')
});
