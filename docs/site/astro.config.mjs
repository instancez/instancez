// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeBlack from 'starlight-theme-black';
import starlightLlmsTxt from 'starlight-llms-txt';
import starlightPageActions from 'starlight-page-actions';
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
  base: '/',
  integrations: [
    starlight({
      plugins: [
        // Kept only for the per-page `.md` routes it generates (what the
        // "Copy page markdown" button copies from). Its own button UI is
        // replaced by the project-level PageTitle + TableOfContents overrides
        // below, which supersede it. No baseUrl on purpose: with it the plugin
        // would emit its own llms.txt and collide with starlight-llms-txt,
        // which already owns that file (plus the full/small dumps).
        starlightPageActions(),
        starlightThemeBlack({
          footerText: 'instancez — a lightweight, self-hosted backend. [GitHub](https://github.com/instancez/instancez)',
        }),
        starlightLlmsTxt({
          projectName: 'instancez',
          description: 'A lightweight, self-hosted backend that speaks the Supabase wire protocol.',
        }),
      ],
      title: 'instancez',
      description: 'A lightweight, self-hosted backend',
      favicon: '/favicon.svg',
      head: [
        // Best-effort hint pointing LLM tooling at the generated llms.txt.
        // There's no standard an agent auto-honors — discovery is really the
        // well-known /llms.txt path — but this is cheap and some tools read it.
        {
          tag: 'link',
          attrs: {
            rel: 'alternate',
            type: 'text/plain',
            title: 'llms.txt',
            href: '/llms.txt',
          },
        },
        ...(docsVersion
          ? [
              {
                tag: 'style',
                content: `.site-title .title-text::after{content:"${docsVersion}";margin-left:.55rem;font-family:'Geist Mono',monospace;font-size:.62rem;font-weight:500;letter-spacing:.02em;color:#6a6a6a;border:1px solid #1c1c1c;border-radius:99px;padding:2px 8px;vertical-align:middle}`,
              },
            ]
          : []),
      ],
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
        { label: 'Coding Agents', slug: 'coding-agents' },
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
            { label: 'Ecommerce Store', slug: 'examples/ecommerce-store' },
            { label: 'File Gallery', slug: 'examples/file-gallery' },
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
        // Own the page-action dropdown and keep theme-black's title. The
        // dropdown lives in this PageTitle override, right-aligned at the top
        // of the content (just left of the ToC rail). Project-level overrides
        // win over both plugins (see plugins comment).
        PageTitle: './src/overrides/PageTitle.astro',
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
