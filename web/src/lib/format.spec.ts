import { describe, expect, it } from 'vitest';
import { formatSize, parseSize } from './format';

describe('formatSize', () => {
	it('shows whole bytes below 1KB', () => {
		// "0.50 KB" reads like a rounding of something bigger when the count is exact.
		expect(formatSize(0)).toBe('0 B');
		expect(formatSize(1)).toBe('1 B');
		expect(formatSize(1023)).toBe('1023 B');
	});

	it('steps up a unit at each 1024 boundary, to two decimals', () => {
		expect(formatSize(1024)).toBe('1.00 KB');
		expect(formatSize(1536)).toBe('1.50 KB');
		expect(formatSize(1024 * 1024)).toBe('1.00 MB');
		expect(formatSize(100 * 1024 * 1024)).toBe('100.00 MB');
		expect(formatSize(1024 ** 3)).toBe('1.00 GB');
		expect(formatSize(1024 ** 4)).toBe('1.00 TB');
	});

	it('stops at the largest unit it knows', () => {
		expect(formatSize(5 * 1024 ** 5)).toBe('5120.00 TB');
	});

	it('refuses values that are not a size', () => {
		expect(formatSize(-1)).toBe('—');
		expect(formatSize(NaN)).toBe('—');
		expect(formatSize(Infinity)).toBe('—');
	});
});

describe('parseSize', () => {
	it('reads a bare number as megabytes', () => {
		// The quota field is labelled in MB and that is what an admin types.
		expect(parseSize('100')).toBe(100 * 1024 * 1024);
		expect(parseSize(' 2 ')).toBe(2 * 1024 * 1024);
	});

	it('accepts an explicit unit in any case, with or without a space', () => {
		expect(parseSize('512KB')).toBe(512 * 1024);
		expect(parseSize('1.5 gb')).toBe(1.5 * 1024 ** 3);
		expect(parseSize('900 B')).toBe(900);
	});

	it('round-trips what formatSize produces', () => {
		// startQuotaEdit pre-fills the field with formatSize's output, so this
		// is what keeps opening the editor and pressing Enter a no-op.
		for (const bytes of [0, 900, 512 * 1024, 100 * 1024 * 1024, 2 * 1024 ** 3]) {
			expect(parseSize(formatSize(bytes))).toBe(bytes);
		}
	});

	it('rejects anything that is not a size', () => {
		expect(parseSize('')).toBeNull();
		expect(parseSize('abc')).toBeNull();
		expect(parseSize('-5')).toBeNull();
		expect(parseSize('10 petabytes')).toBeNull();
	});
});
