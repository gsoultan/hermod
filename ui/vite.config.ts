import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { visualizer } from 'rollup-plugin-visualizer'
import { brotliCompressSync } from 'node:zlib'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
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
    setupFiles: 'src/test/setupTests.ts',
    globals: true,
    css: true,
    include: ['src/__tests__/**/*.test.{ts,tsx}'],
  },
})
