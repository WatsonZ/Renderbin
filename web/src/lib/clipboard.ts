/**
 * Copy text to the clipboard, reporting whether it worked.
 *
 * `navigator.clipboard` only exists in a secure context, and this app is
 * explicitly meant to be self-hostable over plain `http://<lan-ip>`. Calling it
 * unguarded there throws a TypeError into an unhandled rejection, which is how
 * every copy button in the app used to fail: no copy, no error, nothing at all
 * on screen.
 *
 * A deployment behind an HTTPS-terminating Nginx or Caddy is a secure context
 * as far as the browser is concerned, so the modern path is what actually runs
 * in production; the fallback is for the LAN case and for a browser that
 * refuses the permission. When both fail the caller is expected to show the
 * text somewhere selectable — see the copy-failed banner on the dashboard —
 * because a share link the user cannot reach any other way is worse than an
 * ugly one they can.
 */
export async function copyText(text: string): Promise<boolean> {
	if (navigator.clipboard?.writeText) {
		try {
			await navigator.clipboard.writeText(text);
			return true;
		} catch {
			// Fall through: permission denied, or a browser that exposes the
			// API outside a secure context and then refuses to use it.
		}
	}
	return legacyCopy(text);
}

/**
 * `document.execCommand('copy')` is deprecated but still implemented
 * everywhere, and it is the only clipboard write available on a non-secure
 * origin. The textarea has to be in the document and selectable for the
 * command to have a selection to act on; `readonly` keeps the mobile keyboard
 * from opening and the fixed off-screen position keeps the page from scrolling.
 */
function legacyCopy(text: string): boolean {
	const area = document.createElement('textarea');
	area.value = text;
	area.setAttribute('readonly', '');
	area.style.position = 'fixed';
	area.style.top = '-1000px';
	area.style.opacity = '0';
	document.body.appendChild(area);
	try {
		area.select();
		return document.execCommand('copy');
	} catch {
		return false;
	} finally {
		document.body.removeChild(area);
	}
}
