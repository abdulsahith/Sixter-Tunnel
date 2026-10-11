/** @type {import('tailwindcss').Config} */
export default {
  // Scan all source files for class names.
  content: ['./index.html', './src/**/*.{js,jsx}'],
  // The existing app ships its own global reset + design system in styles.css,
  // so we disable Tailwind's preflight to avoid disturbing the working
  // auth / console / admin / docs pages. Utilities still work everywhere.
  corePlugins: { preflight: false },
  theme: {
    extend: {
      colors: {
        // Sixter premium dark palette
        ink: {
          900: '#050816',
          800: '#070a1a',
          700: '#0b1020',
          600: '#0e1426',
        },
        brand: {
          DEFAULT: '#8B7CFF',
          soft: '#A1A0FF',
          deep: '#6A5AE0',
        },
        accent: {
          DEFAULT: '#A855F7',
          soft: '#C084FC',
        },
        sky: { glow: '#57A0F5' },
        ash: '#A1A1AA',
      },
      fontFamily: {
        sans: ['"Plus Jakarta Sans"', 'Inter', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"Cascadia Code"', 'Consolas', 'monospace'],
      },
      borderRadius: {
        '2xl': '20px',
        '3xl': '24px',
        '4xl': '32px',
      },
      boxShadow: {
        glass: '0 20px 80px rgba(139,124,255,0.15)',
        'glass-hover': '0 30px 100px rgba(139,124,255,0.28)',
        glow: '0 0 40px rgba(139,124,255,0.45)',
      },
      backdropBlur: { xl: '20px' },
      maxWidth: { content: '1200px' },
      keyframes: {
        float: {
          '0%,100%': { transform: 'translateY(0)' },
          '50%': { transform: 'translateY(-14px)' },
        },
        'spin-slow': {
          from: { transform: 'rotate(0deg)' },
          to: { transform: 'rotate(360deg)' },
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' },
        },
      },
      animation: {
        float: 'float 7s ease-in-out infinite',
        'spin-slow': 'spin-slow 60s linear infinite',
        shimmer: 'shimmer 3s linear infinite',
      },
    },
  },
  plugins: [],
}
