import node from '@astrojs/node';
import preact from '@astrojs/preact';
import { defineConfig, envField } from 'astro/config';

export default defineConfig({
  output: 'server',
  adapter: node({ mode: 'standalone' }),
  integrations: [preact()],
  server: { host: true, port: 4321 },
  security: {
    allowedDomains: [{ protocol: 'https', hostname: 'ccar-p.buildspace.run' }]
  },
  env: {
    schema: {
      BACKEND_URL: envField.string({ context: 'server', access: 'secret', default: 'http://localhost:8080' }),
      PUBLIC_BASE_URL: envField.string({ context: 'server', access: 'public', default: 'http://localhost:4321' })
    }
  }
});
