import type { Config } from 'tailwindcss'

const config: Config = {
  content: ['./src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        fug:    '#2DE59A',
        hunt:   '#FF4B5C',
        gold:   '#FFC24B',
        violet: '#9D8CFF',
        bg:     '#09090f',
        'bg-1': '#0d0f14',
        'bg-2': '#12141a',
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'sans-serif'],
        mono: ['SF Mono', 'Fira Code', 'monospace'],
      },
    },
  },
  plugins: [],
}

export default config
