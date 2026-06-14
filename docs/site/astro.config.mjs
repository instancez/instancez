// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://instancez.github.io',
  base: '/instancez',
  integrations: [
    starlight({
      title: 'instancez',
      description: 'Self-hosted Supabase-compatible backend',
      favicon: '/favicon.svg',
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
          ],
        },
        {
          label: 'Deploy',
          items: [
            { label: 'Docker', slug: 'deploy/docker' },
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
        SiteTitle: './src/components/SiteTitle.astro',
      },
      customCss: ['./src/styles/custom.css'],
      expressiveCode: {
        themes: ['material-theme-darker'],
      },
    }),
  ],
});
