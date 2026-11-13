import { defineConfig } from 'vitest/config';

// The Go server; in docker compose this is http://server:8080.
const server = process.env.VITE_SERVER ?? 'http://localhost:8080';

export default defineConfig({
  server: {
    host: '0.0.0.0',
    port: 5173,
    strictPort: true,
    proxy: {
      '/join': { target: server, changeOrigin: true },
      '/ws': { target: server, changeOrigin: true, ws: true },
    },
  },
  build: { target: 'es2022' },
  test: { environment: 'node', include: ['src/**/*.test.ts'] },
});
