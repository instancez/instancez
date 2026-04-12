import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import mdx from "@mdx-js/rollup";
import remarkGfm from "remark-gfm";
import remarkFrontmatter from "remark-frontmatter";
import remarkMdxFrontmatter from "remark-mdx-frontmatter";
import rehypePrettyCode from "rehype-pretty-code";

export default defineConfig({
  plugins: [
    mdx({
      providerImportSource: "@mdx-js/react",
      remarkPlugins: [remarkGfm, remarkFrontmatter, remarkMdxFrontmatter],
      rehypePlugins: [
        [
          rehypePrettyCode,
          {
            theme: "github-dark-default",
            keepBackground: true,
          },
        ],
      ],
    }),
    react(),
    tailwindcss(),
  ],
  base: "/docs",
  server: {
    port: 5174,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
