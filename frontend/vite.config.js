import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The production build is written into the Go package that embeds it.
export default defineConfig({
  plugins: [svelte()],
  build: { outDir: '../backend/internal/web/dist', emptyOutDir: true },
  server: { proxy: { '/api': 'http://localhost:8080' } },
});
