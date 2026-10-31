import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The production build is written into the Go package that embeds it.
export default defineConfig({
  plugins: [svelte()],
  build: { outDir: '../backend/internal/web/dist', emptyOutDir: true },
  // /oauth/authorize is a page of the app; the other OAuth endpoints and /mcp belong to the Go server.
  server: {
    proxy: Object.fromEntries(
      ['/api', '/mcp', '/oauth/token', '/oauth/register', '/oauth/revoke', '/.well-known'].map((p) => [p, 'http://localhost:8080']),
    ),
  },
  // Component tests mount the real Svelte components in jsdom with the browser build of Svelte.
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
  test: { environment: 'jsdom', include: ['src/**/*.test.js'] },
});
