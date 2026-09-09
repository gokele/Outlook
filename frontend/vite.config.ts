import { fileURLToPath, URL } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

/**
 * Vite 构建配置。
 * - 前端产物为纯静态文件, 生产环境由 Nginx 与后端同源部署, 请求一律走相对路径 `/api`
 * - 开发期通过 proxy 把 `/api/` 转发到本地 Go 服务, 仅用于本地联调
 */
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // 必须带尾斜杠。Vite 的代理键按前缀匹配, 写成 `/api` 会把 `/apikeys`
      // 这类前端路由也转发到后端, 导致页面 404。
      // Nginx 侧的 `location /api/` 本身就要求尾斜杠, 不存在这个问题。
      '/api/': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        /**
         * 把体积较大且更新频率低的第三方依赖拆成独立 chunk, 提升托管层缓存命中率。
         * 用函数形式而非对象形式, 避免依赖被其它 chunk 提前吸走后生成空 chunk。
         */
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined;
          if (id.includes('@tanstack')) return 'tanstack';
          if (id.includes('/antd/') || id.includes('@ant-design') || id.includes('/rc-')) {
            return 'antd';
          }
          if (id.includes('/react/') || id.includes('/react-dom/') || id.includes('/scheduler/')) {
            return 'react';
          }
          // 其余依赖交给 Rollup 自行归组, 避免人为造出循环 chunk
          return undefined;
        },
      },
    },
  },
});
