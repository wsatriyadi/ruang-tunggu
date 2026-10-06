import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The Go backend serves web/dist as the SPA root and listens on :8080, so the
// dev server only needs to forward the API prefix. Nothing here buffers:
// `/api/events` is an SSE stream and must reach the browser frame by frame.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: false,
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
});