import { expect, test } from '@playwright/test';

test('authentication submits backend JSON and CSRF header', async ({ page }) => {
  await page.route('**/api/v1/auth/login', route => route.fulfill({ json: { data: { csrf_token: 'session-token', user: { id: 'u1' } } } }));
  await page.goto('/login');
  await page.getByLabel('Email address').fill('reader@example.test');
  await page.getByLabel('Password').fill('correct horse battery staple');
  const requestPromise = page.waitForRequest(request => request.url().endsWith('/api/v1/auth/login') && request.method() === 'POST');
  await page.getByRole('button', { name: 'Sign in' }).click();
  const request = await requestPromise;
  expect(request.method()).toBe('POST');
  expect(request.headers()['content-type']).toContain('application/json');
  expect(request.headers()['x-csrf-token']).toBe('anonymous-token');
  expect(request.postDataJSON()).toEqual({ email: 'reader@example.test', password: 'correct horse battery staple' });
});

test('admin exam creation uses snake-case backend payload', async ({ page }) => {
  await page.route('**/api/v1/admin/exams', route => route.request().method() === 'POST'
    ? route.fulfill({ status: 201, json: { data: { id: 'exam-1' } } })
    : route.continue());
  await page.goto('/admin/exams/new');
  await page.getByLabel('Title').fill('Editorial exam');
  await page.getByLabel('Slug').fill('editorial-exam');
  await page.getByLabel('Description').fill('A complete description');
  const requestPromise = page.waitForRequest(request => request.url().endsWith('/api/v1/admin/exams') && request.method() === 'POST');
  await page.getByRole('button', { name: 'Create exam' }).click();
  const payload = (await requestPromise).postDataJSON();
  expect(payload.time_limit_minutes).toBe(60);
  expect(payload.pass_percentage).toBe(70);
  expect(payload.timeLimitMinutes).toBeUndefined();
});
