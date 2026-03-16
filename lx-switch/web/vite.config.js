import { defineConfig } from 'vite';
import { resolve } from 'path';

export default defineConfig({
  root: '.',
  base: '/static/',
  publicDir: false,
  build: {
    outDir: 'static',
    emptyOutDir: false,
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'index.html'),
        login: resolve(__dirname, 'login.html'),
      },
      output: {
        entryFileNames: '[name].js',
        chunkFileNames: 'chunks/[name]-[hash].js',
        assetFileNames: '[name].[ext]',
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:18777',
        changeOrigin: true,
      },
      '/login': {
        target: 'http://localhost:18777',
        changeOrigin: true,
      },
      '/logout': {
        target: 'http://localhost:18777',
        changeOrigin: true,
      },
    },
  },
});
