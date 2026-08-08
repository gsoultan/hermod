import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import { visualizer } from 'rollup-plugin-visualizer'
import { brotliCompressSync } from 'node:zlib'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// Where the dev server listens and which backend it proxies to. The defaults
// are the values scripts/dev.sh and playwright.config.ts expect, so nothing
// changes unless you ask for it. Override them to run a second UI against an
// isolated backend (an E2E run on a throwaway database, say) without stopping
// the stack you are developing in.
const HERMOD_UI_PORT = Number(process.env.HERMOD_UI_PORT ?? 5175)
const HERMOD_API_TARGET = process.env.HERMOD_API_TARGET ?? 'http://localhost:4005'
const HERMOD_WS_TARGET = HERMOD_API_TARGET.replace(/^http/, 'ws')

// https://vite.dev/config/
export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  plugins: [
    react(),
    VitePWA({
      // autoUpdate + skipWaiting + clientsClaim: a new build takes over on the
      // next load with no "waiting worker" state. The classic service-worker
      // failure mode is a user stranded on a stale shell whose chunks have been
      // deleted; this configuration makes that unreachable.
      registerType: 'autoUpdate',
      injectRegister: 'auto',
      workbox: {
        clientsClaim: true,
        skipWaiting: true,
        cleanupOutdatedCaches: true,
        // Precache only the application shell. Hermod monitors live pipelines,
        // so a cached API response would show stale throughput or a stopped
        // workflow as running — actively misleading in an operations tool.
        // Nothing under /api is ever cached, and navigations there are excluded
        // from the SPA fallback.
        globPatterns: ['**/*.{js,css,html,svg,woff2}'],
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [/^\/api/, /^\/streams/, /^\/metrics/, /^\/livez/, /^\/readyz/],
        runtimeCaching: [],
        maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
      },
      manifest: {
        name: 'Hermod',
        short_name: 'Hermod',
        description: 'Enterprise data integration and streaming platform',
        theme_color: '#4c6ef5',
        background_color: '#1a1b1e',
        display: 'standalone',
        start_url: '/',
        icons: [
          { src: '/favicon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
        ],
      },
      devOptions: {
        // Off in dev: a service worker intercepting HMR is a debugging trap.
        enabled: false,
      },
    }),
    visualizer({
      filename: 'stats.html',
      gzipSize: true,
      brotliSize: true,
      template: 'treemap',
    }),
    {
      name: 'brotli-compression',
      apply: 'build',
      enforce: 'post',
      closeBundle() {
        const distDir = path.resolve(__dirname, 'dist');
        const compressFiles = (dir: string) => {
          const files = fs.readdirSync(dir);
          for (const file of files) {
            const filePath = path.join(dir, file);
            const stats = fs.statSync(filePath);
            if (stats.isDirectory()) {
              compressFiles(filePath);
            } else if (
              /\.(js|css|html|svg|json|wasm)$/.test(file) &&
              stats.size > 1024 &&
              !file.endsWith('.br') &&
              !file.endsWith('.gz')
            ) {
              try {
                const content = fs.readFileSync(filePath);
                const compressed = brotliCompressSync(content);
                // Only write if compressed is smaller than original
                if (compressed.length < content.length) {
                  fs.writeFileSync(`${filePath}.br`, compressed);
                }
              } catch (err) {
                console.error(`Failed to compress ${file}:`, err);
              }
            }
          }
        };
        if (fs.existsSync(distDir)) {
          compressFiles(distDir);
        }
      },
    },
  ],
  build: {
    rollupOptions: {
      output: {
        // Isolate heavy libraries into their own async chunks to keep main smaller
        manualChunks(id) {
          const path = id.replace(/\\/g, '/');
          if (
            path.includes('/node_modules/reactflow') ||
            path.includes('/node_modules/dagre') ||
            path.includes('/node_modules/d3-')
          ) {
            return 'reactflow-vendor'
          }
          if (path.includes('/node_modules/@mantine/')) {
            return 'mantine-vendor'
          }
          if (path.includes('/node_modules/@tanstack/')) {
            return 'tanstack-vendor'
          }
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    // jsdom only exposes window.localStorage for non-opaque origins. The default
    // "about:blank" URL is opaque, which leaves localStorage undefined and breaks
    // any component/test relying on persisted auth tokens. Pin a real origin.
    environmentOptions: {
      jsdom: {
        url: 'http://localhost',
      },
    },
    setupFiles: 'src/test/setupTests.ts',
    globals: true,
    css: true,
    include: ['src/__tests__/**/*.test.{ts,tsx}'],
  },
  server: {
    // Pinned so `bun run dev`, scripts/dev.sh and playwright.config.ts all agree
    // on one origin. Left unset it defaulted to 5173 while Playwright expected
    // 5175, so E2E runs silently hit nothing.
    //
    // Both are overridable so a second, isolated stack can be run alongside the
    // usual one — an E2E run against a throwaway database must not have to stop
    // the dev instance you are working in.
    port: HERMOD_UI_PORT,
    strictPort: true,
    proxy: {
      '/api/ws': {
        target: HERMOD_WS_TARGET,
        ws: true,
      },
      '/api': {
        target: HERMOD_API_TARGET,
        changeOrigin: true,
      },
      '/ws': {
        target: HERMOD_WS_TARGET,
        ws: true,
      },
    },
  },
})
