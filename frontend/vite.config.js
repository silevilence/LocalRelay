import {defineConfig} from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // Keep Wails' auto-detected asset proxy on one loopback address instead of
    // relying on Node/Go resolving localhost to the same IP family.
    // https://vite.dev/config/server-options#server-host
    host: '127.0.0.1'
  }
})
