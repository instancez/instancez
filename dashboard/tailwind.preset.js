/**
 * @instancez/console — Tailwind preset stub
 *
 * This package uses Tailwind CSS v4, where the design tokens (colors, radii,
 * shadows, fonts) live in src/index.css via @theme directives rather than in
 * a JavaScript config file. There is no JavaScript theme object to extract.
 *
 * For consumers (Vite + Tailwind v4):
 *   Import the CSS tokens directly in your global stylesheet:
 *     @import "@instancez/console/styles.css";
 *
 * This file exists only to satisfy the "./tailwind-preset" package export so
 * that the entry is resolvable. It exports an empty preset object.
 *
 * @type {import('tailwindcss').Config}
 */
module.exports = {
  theme: {},
  plugins: [],
};
