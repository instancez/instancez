// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeBlack from 'starlight-theme-black';
import { execSync } from 'node:child_process';

// Latest release tag, rendered as a small badge after the site title in the
// docs header. DOCS_VERSION covers tag builds; otherwise we read the newest git
// tag (CI fetches tags via fetch-depth: 0). An empty value renders no badge.
// The header markup comes from starlight-theme-black, which bypasses Starlight's
// SiteTitle slot, so we attach the badge with CSS rather than a component.
let docsVersion = process.env.DOCS_VERSION ?? '';
if (!docsVersion) {
  try {
    docsVersion = execSync('git describe --tags --abbrev=0 2>/dev/null', { encoding: 'utf8' }).trim();
  } catch {}
}

export default defineConfig({
  site: 'https://instancez.github.io',
  base: '/instancez',
  integrations: [
    starlight({
      plugins: [starlightThemeBlack({
        footerText: 'instancez — a lightweight, self-hosted backend. [GitHub](https://github.com/instancez/instancez)',
      })],
      title: 'instancez',
      description: 'A lightweight, self-hosted backend',
      favicon: '/favicon.svg',
      head: docsVersion
        ? [
            {
              tag: 'style',
              content: `.site-title .title-text::after{content:"${docsVersion}";margin-left:.55rem;font-family:'Geist Mono',monospace;font-size:.62rem;font-weight:500;letter-spacing:.02em;color:#6a6a6a;border:1px solid #1c1c1c;border-radius:99px;padding:2px 8px;vertical-align:middle}`,
            },
          ]
        : [],
      logo: {
        src: './src/assets/logo.svg',
        alt: 'instancez',
      },
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/instancez/instancez' },
      ],
      sidebar: [
        { label: 'Quick Start', slug: 'quick-start' },
        { label: 'Installation', slug: 'install' },
        {
          label: 'Build',
          items: [
            { label: 'Tables / Schema', slug: 'build/schema' },
            { label: 'Auth', slug: 'build/auth' },
            { label: 'RLS Policies', slug: 'build/rls' },
            { label: 'SQL Functions', slug: 'api-reference/rpc' },
            { label: 'Code Functions', slug: 'build/functions' },
            { label: 'Storage', slug: 'build/storage' },
            { label: 'Querying', slug: 'build/querying' },
            { label: 'Supabase SDK Compatibility', slug: 'supabase-compatibility' },
          ],
        },
        {
          label: 'Deploy',
          items: [
            { label: 'instancez Cloud', slug: 'deploy/cloud' },
            { label: 'Docker', slug: 'deploy/docker' },
            { label: 'Kubernetes', slug: 'deploy/kubernetes' },
            { label: 'AWS Lambda', slug: 'deploy/lambda' },
            { label: 'Self-hosted', slug: 'deploy/self-hosted' },
            { label: 'Environment Variables', slug: 'deploy/env-vars' },
          ],
        },
        {
          label: 'Examples',
          items: [
            { label: 'Todo App', slug: 'examples/todo-app' },
            { label: 'File Gallery', slug: 'examples/file-gallery' },
            { label: 'Webhook Handler', slug: 'examples/webhook-function' },
          ],
        },
        {
          label: 'CLI Reference',
          items: [
            { label: 'CLI', slug: 'api-reference/cli' },
          ],
        },
        {
          label: 'Configuration',
          items: [
            { label: 'instancez.yaml', slug: 'api-reference/config' },
          ],
        },
      ],
      components: {
        // Dark-only: pin the theme to dark before paint. The visible toggle
        // belongs to the starlight-theme-black plugin and is hidden in CSS.
        ThemeProvider: './src/components/ThemeProvider.astro',
      },
      customCss: [
        '@fontsource/geist-sans/400.css',
        '@fontsource/geist-sans/500.css',
        '@fontsource/geist-sans/600.css',
        '@fontsource/geist-sans/700.css',
        '@fontsource/geist-mono/400.css',
        '@fontsource/geist-mono/500.css',
        './src/styles/custom.css',
      ],
      expressiveCode: {
        themes: ['material-theme-darker'],
      },
    }),
  ],
});
