import { describe, expect, it } from 'vitest';
import { changePasswordSchema, registerSchema } from './register';

const valid = {
	username: 'user_1',
	nickname: 'Nick',
	password: 'secret1',
	rePassword: 'secret1'
};

describe('registerSchema', () => {
	it('accepts a valid payload', () => {
		expect(registerSchema.safeParse(valid).success).toBe(true);
	});

	it('rejects usernames with invalid characters', () => {
		const r = registerSchema.safeParse({ ...valid, username: 'has space' });
		expect(r.success).toBe(false);
		if (!r.success) {
			expect(r.error.issues[0].message).toBe('register.usernameInvalid');
		}
	});

	it('rejects short passwords', () => {
		const r = registerSchema.safeParse({ ...valid, password: 'short', rePassword: 'short' });
		expect(r.success).toBe(false);
		if (!r.success) {
			expect(r.error.issues[0].message).toBe('register.passwordTooShort');
		}
	});

	it('rejects mismatched passwords with the error on rePassword', () => {
		const r = registerSchema.safeParse({ ...valid, rePassword: 'different1' });
		expect(r.success).toBe(false);
		if (!r.success) {
			expect(r.error.issues[0].message).toBe('register.passwordMismatch');
			expect(r.error.issues[0].path).toEqual(['rePassword']);
		}
	});
});

describe('changePasswordSchema', () => {
	it('requires the current password', () => {
		const r = changePasswordSchema.safeParse({
			currentPassword: '',
			password: 'secret1',
			rePassword: 'secret1'
		});
		expect(r.success).toBe(false);
		if (!r.success) {
			expect(r.error.issues[0].message).toBe('settings.currentPasswordRequired');
		}
	});

	it('accepts a valid change', () => {
		const r = changePasswordSchema.safeParse({
			currentPassword: 'old-pass',
			password: 'secret1',
			rePassword: 'secret1'
		});
		expect(r.success).toBe(true);
	});
});
