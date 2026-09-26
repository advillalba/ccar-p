import node from '@astrojs/node';
import preact from '@astrojs/preact';
import { defineConfig, envField } from 'astro/config';

// Static variant of astro.config.mjs, used by .github/workflows/gh-pages.yml.
// output: 'static' prerenders every page that does not export
// `prerender = false`; the Node adapter keeps those on-demand routes working
// when the artifact is served by the SSR entry (dist/server/entry.mjs).
// Deployment target is a GitHub Pages project site: PUBLIC_BASE_PATH carries
// the repository base (e.g. /ccar-p) so asset URLs resolve under it.
export default defineConfig({
  output: 'static',
  adapter: node({ mode: 'standalone' }),
  integrations: [preact()],
  server: { host: true, port: 4321 },
  ...(process.env.PUBLIC_BASE_PATH ? { base: process.env.PUBLIC_BASE_PATH } : {}),
  env: {
    schema: {
      BACKEND_URL: envField.string({ context: 'server', access: 'secret', default: 'http://localhost:8080' }),
      PUBLIC_BASE_URL: envField.string({ context: 'server', access: 'public', default: 'http://localhost:4321' })
    }
  }
});
