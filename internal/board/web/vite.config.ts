import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath } from 'node:url'

const entry = fileURLToPath(new URL('./src/main.ts', import.meta.url))
const outDir = fileURLToPath(new URL('../assets/dist', import.meta.url))

// 构建为 IIFE：产物 internal/board/assets/dist/board-web.js，全局名 `MawBoard`。
export default defineConfig({
  plugins: [vue()],
  define: {
    'process.env.NODE_ENV': JSON.stringify('production'),
  },
  build: {
    outDir,
    emptyOutDir: true,
    target: 'es2022',
    sourcemap: false,
    lib: {
      entry,
      name: 'MawBoard',
      formats: ['iife'],
      fileName: () => 'board-web.js',
    },
    rollupOptions: {
      output: {
        inlineDynamicImports: true,
      },
    },
  },
})
