import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const publicRoutes = ['/', '/notes', '/exams', '/practice', '/practice/beginner', '/practice/exam_scenarios', '/login', '/register', '/admin', '/admin/notes', '/admin/notes/new', '/admin/exams', '/admin/exams/new', '/admin/publications', '/attempts'];

for (const route of publicRoutes) {
  test(`${route} is semantic, keyboard reachable, and narrow`, async ({ page }) => {
    await page.goto(route);
    await expect(page.locator('main')).toBeVisible();
    await page.keyboard.press('Tab');
    await expect(page.locator(':focus')).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(1);
    const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze();
    expect(scan.violations).toEqual([]);
  });
}

test.describe('note navigation', () => {
  test('table of contents anchors stay inside the note', async ({ page }) => {
    await page.goto('/notes/booked');
    const tocLink = page.locator('.toc a[href="#second"]');
    await expect(tocLink).toBeVisible();
    await tocLink.click();
    await expect(page).toHaveURL(/#second$/);
    await expect(page.locator('#second')).toBeVisible();
  });

  test('previous and next note links traverse the published reading list', async ({ page }) => {
    await page.goto('/notes/booked');
    await page.locator('a[rel="prev"]').click();
    await expect(page).toHaveURL(/\/notes\/long-note$/);
    await page.goto('/notes/booked');
    await page.locator('a[rel="next"]').click();
    await expect(page).toHaveURL(/\/notes\/outro$/);
  });
});

test.describe('without JavaScript', () => {
  test.use({ javaScriptEnabled: false });

  test('public reading and navigation remain useful', async ({ page }) => {
    await page.goto('/notes');
    await expect(page.getByRole('heading', { name: 'Study notes' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Exams' })).toHaveAttribute('href', '/exams');
  });

  test('exam start states the interactive limitation', async ({ page }) => {
    await page.goto('/exams/sample/start');
    await expect(page.getByRole('heading', { name: 'Completing the exam' })).toBeVisible();
  });

  test('note reading keeps its no-script paths', async ({ page }) => {
    await page.goto('/notes/booked');
    await expect(page.locator('a[rel="prev"]')).toHaveAttribute('href', '/notes/long-note');
    await expect(page.locator('a[rel="next"]')).toHaveAttribute('href', '/notes/outro');
    await expect(page.locator('.toc a[href="#first"]')).toBeVisible();
    await expect(page.locator('#first')).toBeVisible();
    await expect(page.locator('script[src="/reading-nav.js"]')).toHaveCount(0);
    await expect(page.locator('script[src="/reading-pages.js"]')).toHaveCount(0);
    await page.goto('/notes');
    await expect(page.locator('script[src="/reading-nav.js"]')).toHaveCount(0);
    await expect(page.locator('script[src="/reading-pages.js"]')).toHaveCount(0);
  });
});

test('nested note envelope renders sanitized HTML without overflow', async ({ page }) => {
  await page.goto('/notes/long-note');
  await expect(page.getByRole('heading', { name: 'Safe heading' })).toBeVisible();
  await expect(page.getByText('<script>window.leaked=true</script>')).toBeVisible();
  expect(await page.locator('.prose script').count()).toBe(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
});

test('public exam source never contains answer-key fields', async ({ page }) => {
  const response = await page.goto('/exams/confidential');
  const source = await response.text();
  expect(source).not.toMatch(/"(?:is_correct|correct_option_id|explanation)"\s*:/i);
});

test('representative public payload stays within the MVP budget', async ({ page }) => {
  const responses = [];
  page.on('response', response => responses.push(response));
  await page.goto('/');
  let total = 0;
  for (const response of responses) {
    if (!response.url().startsWith('http://127.0.0.1:4322')) continue;
    total += (await response.request().sizes()).responseBodySize;
  }
  expect(total).toBeLessThan(100 * 1024);
  expect(await page.locator('video, canvas, [style*="animation"], script[src*="react"], script[src*="vue"]').count()).toBe(0);
});

test('practice still works when storage is unavailable', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, 'localStorage', { get: () => { throw new Error('blocked'); } });
  });
  await page.goto('/practice/beginner');
  await expect(page.locator('.practice-runner')).toBeVisible();
  await expect(page.locator('h2.prompt')).toHaveText('Sample practice question one');
  await page.getByRole('checkbox').first().check();
  await page.getByRole('button', { name: 'Check answer' }).click();
  await expect(page.locator('.verdict--incorrect')).toHaveText('Incorrect');
  await expect(page.locator('.progress__meta')).toContainText('1 of 3 practiced');
});

test.describe('continuous practice', () => {
  test('has a top-level practice hub with difficulty levels', async ({ page }) => {
    await page.goto('/practice');
    await expect(page.getByRole('heading', { name: 'Continuous practice' })).toBeVisible();
    await expect(page.locator('.app-header__nav a[href="/practice"]')).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('a[href="/practice/beginner"]')).toBeVisible();
    await expect(page.locator('a[href="/practice/intermediate"]')).toBeVisible();
    await expect(page.locator('a[href="/practice/advanced"]')).toBeVisible();
    await expect(page.locator('a[href="/practice/exam_scenarios"]')).toBeVisible();
  });

  test('grades the answer, tracks progress and stores it in this browser', async ({ page }) => {
    await page.goto('/practice/beginner');
    await expect(page.locator('.practice-runner')).toBeVisible();
    await expect(page.locator('h2.prompt')).toHaveText('Sample practice question one');
    await expect(page.locator('.progress__meta')).toContainText('0 of 3 practiced');
    await page.getByRole('checkbox').first().check();
    await page.getByRole('button', { name: 'Check answer' }).click();
    await expect(page.locator('.verdict--incorrect')).toHaveText('Incorrect');
    await expect(page.getByText('The correct answer is B.')).toBeVisible();
    await expect(page.locator('.callout').first()).toContainText('Sample explanation');
    await expect(page.locator('.opt--incorrect')).toBeVisible();
    await expect(page.locator('.progress__meta')).toContainText('1 of 3 practiced');
    await expect(page.locator('.progress__meta')).toContainText('0 correct (0%)');
    await expect(page.getByRole('button', { name: 'Next question' })).toBeVisible();
    const stored = await page.evaluate(() => window.localStorage.getItem('ccarp.practice.beginner.v2'));
    expect(stored).toContain('q1-sample');
  });

  test('resumes, completes and can start again', async ({ page }) => {
    await page.goto('/practice/beginner');
    await expect(page.locator('.practice-runner')).toBeVisible();
    await page.getByRole('checkbox').first().check();
    await page.getByRole('button', { name: 'Check answer' }).click();
    await expect(page.locator('.practice-runner__graded')).toBeVisible();
    await page.getByRole('button', { name: 'Next question' }).click();
    await expect(page.locator('h2.prompt')).toHaveText('Sample practice question two');
    await expect(page.locator('.progress__meta')).toContainText('1 of 3 practiced');
    await page.getByRole('checkbox').first().check();
    await page.getByRole('button', { name: 'Check answer' }).click();
    await expect(page.locator('.practice-runner__graded')).toBeVisible();
    await page.getByRole('button', { name: 'Next question' }).click();
    await expect(page.locator('h2.prompt')).toHaveText('Sample practice question three');
    await page.getByRole('checkbox').nth(1).check();
    await page.getByRole('checkbox').nth(2).check();
    await page.getByRole('button', { name: 'Check answer' }).click();
    await expect(page.locator('.verdict--correct')).toHaveText('Correct');
    await expect(page.getByText('The correct answers are B and C.')).toBeVisible();
    await expect(page.locator('.progress__meta')).toContainText('3 of 3 practiced');
    await expect(page.locator('.progress__meta')).toContainText('1 correct (33%)');
    await page.getByRole('button', { name: 'Next question' }).click();
    await expect(page.locator('.practice-runner__done')).toContainText('All 3 practiced');
    await expect(page.locator('.practice-runner__score')).toContainText('1 of 3 correct');
    await page.reload();
    await expect(page.locator('.practice-runner__done')).toBeVisible();
    await page.getByRole('button', { name: 'Start again' }).click();
    await expect(page.locator('h2.prompt')).toHaveText('Sample practice question one');
    await expect(page.locator('.progress__meta')).toContainText('0 of 3 practiced');
    const stored = await page.evaluate(() => window.localStorage.getItem('ccarp.practice.beginner.v2'));
    expect(JSON.parse(stored).results).toEqual({});
  });
});
