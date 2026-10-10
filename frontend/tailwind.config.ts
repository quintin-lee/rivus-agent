import type { Config } from 'tailwindcss'

function withOpacity(variable: string) {
  return `hsl(var(${variable}) / <alpha-value>)`
}

export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        background: withOpacity('--background'),
        foreground: withOpacity('--foreground'),
        card: { DEFAULT: withOpacity('--card'), foreground: withOpacity('--card-foreground') },
        muted: { DEFAULT: withOpacity('--muted'), foreground: withOpacity('--muted-foreground') },
        border: withOpacity('--border'),
        input: withOpacity('--input'),
        primary: { DEFAULT: withOpacity('--primary'), foreground: withOpacity('--primary-foreground') },
        secondary: { DEFAULT: withOpacity('--secondary'), foreground: withOpacity('--secondary-foreground') },
        accent: { DEFAULT: withOpacity('--accent'), foreground: withOpacity('--accent-foreground') },
        destructive: { DEFAULT: withOpacity('--destructive'), foreground: withOpacity('--destructive-foreground') },
        ring: withOpacity('--ring'),
      },
      borderRadius: { lg: '0.5rem', md: '0.375rem', sm: '0.25rem' },
    },
  },
  plugins: [],
} satisfies Config
