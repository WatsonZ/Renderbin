import { afterEach, describe, expect, it, vi } from 'vitest';
import { copyText } from './clipboard';

/**
 * The whole point of this module is that it must not throw where
 * `navigator.clipboard` is absent — a plain-HTTP LAN deployment, which this app
 * explicitly supports. An unguarded call there produced an unhandled rejection
 * and a copy button that did nothing at all, silently.
 */
function stubDom(opts: { clipboard?: boolean; clipboardThrows?: boolean; execResult?: boolean }) {
	const area = {
		value: '',
		style: {} as Record<string, string>,
		setAttribute: vi.fn(),
		select: vi.fn()
	};
	vi.stubGlobal('document', {
		createElement: () => area,
		body: { appendChild: vi.fn(), removeChild: vi.fn() },
		execCommand: vi.fn(() => opts.execResult ?? false)
	});
	vi.stubGlobal('navigator', {
		clipboard: opts.clipboard
			? {
					writeText: vi.fn(() =>
						opts.clipboardThrows ? Promise.reject(new Error('denied')) : Promise.resolve()
					)
				}
			: undefined
	});
	return area;
}

afterEach(() => vi.unstubAllGlobals());

describe('copyText', () => {
	it('uses the clipboard API when it is available', async () => {
		stubDom({ clipboard: true });
		await expect(copyText('hello')).resolves.toBe(true);
		expect(document.execCommand).not.toHaveBeenCalled();
	});

	it('falls back to execCommand when the API is missing', async () => {
		const area = stubDom({ clipboard: false, execResult: true });
		await expect(copyText('hello')).resolves.toBe(true);
		expect(area.value).toBe('hello');
		expect(document.execCommand).toHaveBeenCalledWith('copy');
	});

	it('falls back when the API exists but refuses', async () => {
		stubDom({ clipboard: true, clipboardThrows: true, execResult: true });
		await expect(copyText('hello')).resolves.toBe(true);
		expect(document.execCommand).toHaveBeenCalled();
	});

	it('reports failure instead of throwing when both paths fail', async () => {
		// The caller shows the text somewhere selectable on false; an
		// exception here would be an unhandled rejection and a dead button.
		stubDom({ clipboard: false, execResult: false });
		await expect(copyText('hello')).resolves.toBe(false);
	});
});
