import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			// SPA build: single fallback HTML page served for all routes, client-side
			// routing takes over from there (see src/routes/+layout.ts for ssr: false).
			adapter: adapter({
				pages: 'build',
				assets: 'build',
				fallback: 'index.html',
				precompress: false,
				strict: true
			})
		})
	],
	server: {
		port: 5173,
		proxy: {
			// forwards API calls (and rendered file pages + the MCP endpoint) to
			// the Go backend during `pnpm dev`
			'/api': 'http://localhost:8080',
			'/res': 'http://localhost:8080',
			'/mcp': 'http://localhost:8080'
		}
	},
	test: {
		expect: { requireAssertions: true },
		environment: 'node',
		include: ['src/**/*.{test,spec}.{js,ts}']
	}
});
