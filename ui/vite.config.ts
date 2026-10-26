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
        // png included so the install-prompt icons are available offline; they
        // were excluded, which left a freshly installed app with no icon until
        // it next reached the network.
        globPatterns: ['**/*.{js,css,html,svg,png,woff2}'],
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [/^\/api/, /^\/streams/, /^\/metrics/, /^\/livez/, /^\/readyz/],
        // Deliberately empty. The precache above covers the whole shell, and
        // every asset URL is content-hashed, so Workbox stores them with
        // revision:null and a deploy re-fetches only the chunks whose hash
        // actually changed — not the full 2.3MB. Anything dynamic here is live
        // pipeline state, where a cached answer would show a stopped workflow
        // as running.
        runtimeCaching: [],
        maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
      },
      manifest: {
        // An explicit id pins the app's identity. Derived from start_url
        // otherwise, so changing start_url later would register a *new* app and
        // orphan everyone's existing install.
        id: '/',
        name: 'Hermod',
        short_name: 'Hermod',
        description: 'Enterprise data integration and streaming platform',
        // Matches the pre-paint background in index.html, so the splash screen
        // and the first painted frame are the same colour.
        theme_color: '#1a1b1e',
        background_color: '#1a1b1e',
        display: 'standalone',
        orientation: 'any',
        start_url: '/',
        scope: '/',
        categories: ['productivity', 'developer', 'utilities'],
        icons: [
          // Raster entries first: an SVG alone satisfies Chrome but leaves
          // Windows tiles and several Android launchers without an icon.
          { src: '/pwa-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
          { src: '/pwa-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
          // Without a maskable entry Android draws the "any" icon on a white
          // plate and letterboxes it. This one keeps its glyph inside the inner
          // 80% safe zone so every mask shape crops cleanly.
          { src: '/pwa-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
          { src: '/favicon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
        ],
        // Real captures of a running instance (ui/scripts/visual-sweep.mjs),
        // one per form factor. Without these Chrome shows its minimal install
        // prompt instead of the richer dialog.
        screenshots: [
          {
            src: '/screenshots/editor-wide.png',
            sizes: '1280x800',
            type: 'image/png',
            form_factor: 'wide',
            label: 'Workflow editor with a running pipeline',
          },
          {
            src: '/screenshots/workflows-narrow.png',
            sizes: '390x844',
            type: 'image/png',
            form_factor: 'narrow',
            label: 'Workflows list on a phone',
          },
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
        // No manualChunks — deliberately, and measured.
        //
        // There used to be hand-rolled buckets here: reactflow-vendor,
        // mantine-vendor, tanstack-vendor. Two problems.
        //
        // First, the reactflow rule matched `/node_modules/reactflow`, but the
        // package was renamed to `@xyflow/react` long ago, so the rule only ever
        // caught dagre and d3 while the editor library itself landed in an
        // anonymous chunk.
        //
        // Second, and worse: forcing modules into a shared bucket promotes the
        // whole bucket to the entry as soon as *any* member is reachable
        // eagerly. That put the workflow editor's graph library and the
        // drag-and-drop kit on the critical path of the login screen. Fixing the
        // rule name made it worse, not better — 1.45MB preloaded.
        //
        // Rolldown's own splitting tracks the eager/lazy boundary properly.
        // Measured on the login route, bytes referenced by index.html:
        //
        //   hand-rolled buckets   1,092,246 raw / ~243 kB brotli /  6 files
        //   automatic             755,000   raw /  ~172 kB brotli / 22 files
        //
        // 31% fewer bytes before first paint. The extra files are all
        // preloaded in parallel and cache at a finer grain, so a change to one
        // component no longer invalidates a 466kB vendor bucket.
        //
        // If you add a bucket here, re-measure. Grouping is not free.
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
