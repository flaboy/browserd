import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  root: fileURLToPath(new URL('./web/browser-live', import.meta.url)),
  base: '/browser-live/',
  build: {
    outDir: fileURLToPath(new URL('./internal/liveviewer/dist', import.meta.url)),
    emptyOutDir: true
  }
})
