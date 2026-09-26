export const prerender = false;

import type { APIRoute } from 'astro';
import { BACKEND_URL } from 'astro:env/server';

export const GET: APIRoute = async () => {
  try {
    const response = await fetch(`${BACKEND_URL}/readyz`, { signal: AbortSignal.timeout(2000) });
    return new Response(response.ok ? 'ok\n' : 'unavailable\n', { status: response.ok ? 200 : 503 });
  } catch {
    return new Response('unavailable\n', { status: 503 });
  }
};
