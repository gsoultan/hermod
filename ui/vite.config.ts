import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { visualizer } from 'rollup-plugin-visualizer'

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
