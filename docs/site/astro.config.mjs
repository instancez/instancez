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
            { label: 'Functions', slug: 'build/functions' },
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
          label: 'API Reference',
          items: [
            { label: 'REST API', slug: 'api-reference/rest' },
            { label: 'Auth API', slug: 'api-reference/auth' },
            { label: 'RPC', slug: 'api-reference/rpc' },
            { label: 'Functions API', slug: 'api-reference/functions' },
            { label: 'Storage API', slug: 'api-reference/storage' },
            { label: 'CLI', slug: 'api-reference/cli' },
            { label: 'Configuration', slug: 'api-reference/config' },
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
