import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const entry = fileURLToPath(new URL('./src/main.ts', import.meta.url))
const outDir = fileURLToPath(new URL('../assets/dist', import.meta.url))

// 构建为 IIFE 入口 bundle：产物 internal/board/assets/dist/board-web.js，
// 全局名 `MawBoard`，供后续 VN-707 通过 embed.FS + /assets 静态挂载接入。
export default defineConfig({
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
  },
})
