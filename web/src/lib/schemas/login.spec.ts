import { describe, expect, it } from 'vitest';
import { loginSchema } from './login';

describe('loginSchema', () => {
	it('accepts a non-empty username and password', () => {
		const result = loginSchema.parse({ username: 'admin', password: 'hunter2' });
		expect(result).toEqual({ username: 'admin', password: 'hunter2' });
	});

	it('rejects an empty username', () => {
		expect(() => loginSchema.parse({ username: '  ', password: 'hunter2' })).toThrow();
	});

	it('rejects an empty password', () => {
		expect(() => loginSchema.parse({ username: 'admin', password: '' })).toThrow();
	});
});
