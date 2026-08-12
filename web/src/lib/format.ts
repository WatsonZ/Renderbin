const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const;

/**
 * Format a byte count with the largest unit that keeps the number above 1,
 * to two decimal places.
 *
 * Bytes are shown whole: "512.00 B" reads like a rounding of something bigger
 * when it is an exact count.
 */
export function formatSize(bytes: number): string {
	if (!Number.isFinite(bytes) || bytes < 0) return '—';
	if (bytes < 1024) return `${Math.round(bytes)} B`;

	let value = bytes;
	let unit = 0;
	while (value >= 1024 && unit < UNITS.length - 1) {
		value /= 1024;
		unit++;
	}
	return `${value.toFixed(2)} ${UNITS[unit]}`;
}

/**
 * Parse a human-typed size ("100", "100MB", "1.5 gb") into bytes, or null when
 * it isn't one. A bare number is megabytes, which is the unit the quota field
 * is labelled in and the one an admin is most likely to type.
 */
export function parseSize(input: string): number | null {
	const m = /^\s*([0-9]+(?:\.[0-9]+)?)\s*(b|kb|mb|gb|tb)?\s*$/i.exec(input);
	if (!m) return null;
	const value = Number(m[1]);
	if (!Number.isFinite(value)) return null;
	const unit = (m[2] ?? 'mb').toUpperCase();
	const exponent = UNITS.indexOf(unit as (typeof UNITS)[number]);
	if (exponent < 0) return null;
	return Math.round(value * 1024 ** exponent);
}
