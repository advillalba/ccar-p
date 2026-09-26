export const prerender = false;

import type { APIRoute } from 'astro';
import { BACKEND_URL } from 'astro:env/server';

const proxy: APIRoute = async ({ request, params }) => {
  const source = new URL(request.url);
  const target = new URL(`/api/${params.path ?? ''}${source.search}`, BACKEND_URL);
  const headers = new Headers(request.headers);
  headers.delete('host');
  const init: RequestInit & { duplex?: 'half' } = {
    method: request.method,
    headers,
    body: request.method === 'GET' || request.method === 'HEAD' ? undefined : request.body,
    redirect: 'manual',
    duplex: 'half'
  };
  const response = await fetch(target, init);
  return new Response(response.body, { status: response.status, headers: response.headers });
};

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
export const OPTIONS = proxy;
