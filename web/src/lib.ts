import { BACKEND_URL } from 'astro:env/server';

export type NoteStatus = 'draft' | 'published' | 'archived';
export type AttemptStatus = 'in_progress' | 'completed';
export type NavCurrent = 'home' | 'notes' | 'exams' | 'practice' | 'attempts' | null;

export interface BreadcrumbItem {
  label: string;
  href?: string;
}

export interface Domain {
  id: string;
  name: string;
  slug: string;
  weight: number;
  sort_order: number;
  is_active?: boolean;
}

export interface NoteTag {
  name: string;
}

export interface NoteReference {
  title: string;
  url?: string;
  citation?: string;
  position: number;
}

export interface NoteTOCItem {
  id: string;
  text: string;
  level: number;
}

export interface AdjacentNote {
  slug: string;
  title: string;
}

export interface NoteSummary {
  id: string;
  slug: string;
  title: string;
  summary: string;
  domain_id?: string;
  domain_name?: string;
  domain_slug?: string;
  domain?: Domain;
  reading_time_minutes: number;
  tags: NoteTag[];
  published_at?: string;
  updated_at?: string;
}

export interface Note extends NoteSummary {
  html?: string;
  markdown?: string;
  references: NoteReference[];
  toc?: NoteTOCItem[];
  previous: AdjacentNote | null;
  next: AdjacentNote | null;
}

export interface ExamSummary {
  id: string;
  slug: string;
  title: string;
  description: string;
  question_count: number;
  domains: Domain[];
  difficulty: string;
  time_limit_minutes: number;
  pass_percentage?: number;
  published_at?: string;
}

export interface ExamOption {
  id?: string;
  key: string;
  text: string;
  position: number;
  // Populated by the static practice site (data/dump.ts); the API attempts
  // endpoint provides explanations through AttemptAnswerOption instead.
  explanation?: string;
  // Marks the correct options; only set on the static practice site.
  is_correct?: boolean;
}

export interface ExamQuestion {
  id: string;
  domain_id?: string;
  prompt: string;
  scenario?: string;
  difficulty?: string;
  position: number;
  options: ExamOption[];
}

export interface Exam extends ExamSummary {
  questions: ExamQuestion[];
}

export interface AttemptSummary {
  id: string;
  exam_id: string;
  exam_slug: string;
  exam_title: string;
  status: AttemptStatus;
  started_at: string;
  completed_at?: string;
  submitted_at?: string;
  question_count: number;
  total_questions: number;
  answered_count?: number;
  score_percentage?: number;
  correct_count?: number;
  passed?: boolean;
  pass_percentage?: number;
}

export interface AttemptAnswerOption {
  id: string;
  key: string;
  text: string;
  position: number;
  explanation?: string;
}

export interface AttemptAnswer {
  id: string;
  prompt: string;
  scenario?: string;
  position: number;
  domain_id?: string;
  options: AttemptAnswerOption[];
  selected_option_ids?: string[];
  correct_option_ids?: string[];
  is_correct?: boolean;
  explanation?: string;
  references?: NoteReference[];
}

export interface AttemptDetail extends AttemptSummary {
  exam?: {
    id: string;
    slug: string;
    title: string;
    pass_percentage?: number;
  };
  questions: AttemptAnswer[];
}

export interface PracticeQuestion {
  id: string;
  // Static practice site (gradingMode: 'local') omits exam_slug: grading
  // compares the selection against the options' is_correct flags instead.
  exam_slug?: string;
  domain_id?: string;
  prompt: string;
  scenario?: string;
  difficulty: string;
  position: number;
  // General question explanation; only set on the static practice site
  // (the API carries it on the PracticeFeedback, not on the question).
  explanation?: string;
  options: ExamOption[];
}

export interface PracticeFeedbackOption {
  id: string;
  key: string;
  text: string;
  explanation: string;
  position: number;
}

export interface PracticeFeedback {
  id: string;
  position: number;
  domain_id?: string;
  prompt: string;
  scenario?: string;
  difficulty?: string;
  explanation: string;
  options: PracticeFeedbackOption[];
  references: NoteReference[];
  selected_option_ids: string[];
  correct_option_ids: string[];
  is_correct: boolean;
}

export interface APIResult<T> {
  data: T | null;
  status: number;
  ok: boolean;
  error?: APIError;
}

export interface APIError {
  code: string;
  message: string;
  fields?: Record<string, string>;
  request_id?: string;
}

const DEFAULT_TIMEOUT_MS = 5000;
const nestedKeys = new Set(['notes', 'note', 'exams', 'exam', 'attempts', 'domains', 'publications', 'questions']);

function unwrapData(value: unknown): unknown {
  let current = value;
  if (current && typeof current === 'object' && !Array.isArray(current) && 'data' in current) {
    current = (current as { data: unknown }).data;
  }
  while (current && typeof current === 'object' && !Array.isArray(current)) {
    const keys = Object.keys(current);
    if (keys.length !== 1 || !nestedKeys.has(keys[0])) break;
    current = (current as Record<string, unknown>)[keys[0]];
  }
  return current;
}

async function readJSON(response: Response): Promise<unknown> {
  if (response.status === 204) return null;
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

async function apiRequest<T>(path: string, opts: { method?: string; cookie?: string | null; body?: unknown; csrf?: string | null; timeoutMs?: number } = {}): Promise<APIResult<T>> {
  const headers = new Headers({ accept: 'application/json' });
  if (opts.cookie) headers.set('cookie', opts.cookie);
  if (opts.body !== undefined) headers.set('content-type', 'application/json');
  if (opts.csrf) headers.set('x-csrf-token', opts.csrf);
  try {
    const response = await fetch(`${BACKEND_URL}${path}`, {
      method: opts.method ?? 'GET',
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: AbortSignal.timeout(opts.timeoutMs ?? DEFAULT_TIMEOUT_MS),
      redirect: 'manual'
    });
    const payload = await readJSON(response);
    if (response.ok) return { data: unwrapData(payload) as T, status: response.status, ok: true };
    const error = payload && typeof payload === 'object' && 'error' in payload ? (payload as { error: APIError }).error : undefined;
    return { data: null, status: response.status, ok: false, error };
  } catch {
    return { data: null, status: 0, ok: false };
  }
}

export async function apiGet<T>(path: string, cookie?: string | null): Promise<APIResult<T>> {
  return apiRequest<T>(path, { cookie });
}

export async function apiPost<T>(path: string, body: unknown, cookie?: string | null, csrf?: string | null): Promise<APIResult<T>> {
  return apiRequest<T>(path, { method: 'POST', cookie, body, csrf });
}

export interface APICookieResult<T> {
  result: APIResult<T>;
  setCookies: string[];
}

// apiPostWithCookies mirrors apiPost but also surfaces the Set-Cookie
// headers the backend issues (used to establish an anonymous guest attempt
// cookie when no session exists).
export async function apiPostWithCookies<T>(path: string, body: unknown, cookie?: string | null, csrf?: string | null): Promise<APICookieResult<T>> {
  const headers = new Headers({ accept: 'application/json' });
  if (cookie) headers.set('cookie', cookie);
  if (body !== undefined) headers.set('content-type', 'application/json');
  if (csrf) headers.set('x-csrf-token', csrf);
  try {
    const response = await fetch(`${BACKEND_URL}${path}`, {
      method: 'POST',
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(DEFAULT_TIMEOUT_MS),
      redirect: 'manual'
    });
    const payload = await readJSON(response);
    const setCookies = response.headers.getSetCookie?.() ?? [];
    if (response.ok) return { result: { data: unwrapData(payload) as T, status: response.status, ok: true }, setCookies };
    const error = payload && typeof payload === 'object' && 'error' in payload ? (payload as { error: APIError }).error : undefined;
    return { result: { data: null, status: response.status, ok: false, error }, setCookies };
  } catch {
    return { result: { data: null, status: 0, ok: false }, setCookies: [] };
  }
}

export function cookieFromRequest(request: Request): string | null {
  const cookie = request.headers.get('cookie');
  return cookie && cookie.length > 0 ? cookie : null;
}

export function cookieValue(request: Request, name: string): string {
  const cookie = request.headers.get('cookie') ?? '';
  for (const part of cookie.split(';')) {
    const [key, ...value] = part.trim().split('=');
    if (key === name) return decodeURIComponent(value.join('='));
  }
  return '';
}

export type MarkdownBlock =
  | { kind: 'heading'; level: number; text: string; id: string }
  | { kind: 'paragraph'; text: string }
  | { kind: 'code'; text: string }
  | { kind: 'quote'; text: string }
  | { kind: 'list'; ordered: boolean; items: string[] };

export function markdownBlocks(source: string): MarkdownBlock[] {
  const lines = source.replace(/\r\n?/g, '\n').split('\n');
  const blocks: MarkdownBlock[] = [];
  let paragraph: string[] = [];
  let code: string[] | null = null;
  let list: { ordered: boolean; items: string[] } | null = null;
  const flushParagraph = () => {
    if (paragraph.length) blocks.push({ kind: 'paragraph', text: paragraph.join(' ') });
    paragraph = [];
  };
  const flushList = () => {
    if (list) blocks.push({ kind: 'list', ordered: list.ordered, items: list.items });
    list = null;
  };
  for (const line of lines) {
    if (line.trim().startsWith('```')) {
      flushParagraph();
      flushList();
      if (code) {
        blocks.push({ kind: 'code', text: code.join('\n') });
        code = null;
      } else {
        code = [];
      }
      continue;
    }
    if (code) {
      code.push(line);
      continue;
    }
    const heading = /^(#{1,4})\s+(.+)$/.exec(line);
    const item = /^\s*(?:(\d+)\.|[-*])\s+(.+)$/.exec(line);
    if (heading) {
      flushParagraph();
      flushList();
      const text = heading[2].trim();
      const id = text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'section';
      blocks.push({ kind: 'heading', level: heading[1].length, text, id });
    } else if (item) {
      flushParagraph();
      const ordered = Boolean(item[1]);
      if (!list || list.ordered !== ordered) flushList();
      list ??= { ordered, items: [] };
      list.items.push(item[2]);
    } else if (line.startsWith('> ')) {
      flushParagraph();
      flushList();
      blocks.push({ kind: 'quote', text: line.slice(2) });
    } else if (!line.trim()) {
      flushParagraph();
      flushList();
    } else {
      paragraph.push(line.trim());
    }
  }
  if (code) blocks.push({ kind: 'code', text: code.join('\n') });
  flushParagraph();
  flushList();
  return blocks;
}

export function normalizeNote<T extends NoteSummary>(note: T): T {
  if (!note.domain) return note;
  return { ...note, domain_id: note.domain.id, domain_name: note.domain.name, domain_slug: note.domain.slug };
}

export function normalizeAttempt(attempt: AttemptDetail): AttemptDetail {
  const questionCount = attempt.question_count ?? attempt.questions?.length ?? 0;
  return {
    ...attempt,
    exam_id: attempt.exam_id ?? attempt.exam?.id ?? '',
    exam_slug: attempt.exam_slug ?? attempt.exam?.slug ?? '',
    exam_title: attempt.exam_title ?? attempt.exam?.title ?? 'Practice exam',
    pass_percentage: attempt.pass_percentage ?? attempt.exam?.pass_percentage,
    submitted_at: attempt.submitted_at ?? attempt.completed_at,
    question_count: questionCount,
    total_questions: attempt.total_questions ?? questionCount,
    answered_count: attempt.answered_count ?? attempt.questions?.filter(question => (question.selected_option_ids?.length ?? 0) > 0).length ?? 0
  };
}

export function normalizeAttemptSummary(attempt: AttemptSummary): AttemptSummary {
  return {
    ...attempt,
    submitted_at: attempt.submitted_at ?? attempt.completed_at,
    total_questions: attempt.total_questions ?? attempt.question_count
  };
}

export function formatDate(value: string | Date | undefined | null): string {
  if (!value) return '';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return new Intl.DateTimeFormat('en', { dateStyle: 'long' }).format(date);
}

export function formatReadingTime(minutes: number | undefined | null): string {
  if (!minutes || minutes < 1) return 'Quick read';
  return minutes === 1 ? '1 minute read' : `${minutes} minute read`;
}

export function formatTimeLimit(minutes: number | undefined | null): string {
  if (!minutes || minutes <= 0) return 'No time limit';
  if (minutes < 60) return `${minutes} minutes`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  if (rest === 0) return hours === 1 ? '1 hour' : `${hours} hours`;
  return `${hours} hour${hours === 1 ? '' : 's'} ${rest} min`;
}

export function formatDifficulty(value: string | undefined | null): string {
  if (!value) return '';
  return value.split(/[_-]+/).filter(Boolean).map(part => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase()).join(' ');
}

export function formatPercentage(value: number | undefined | null, fractionDigits = 0): string {
  if (value === undefined || value === null || Number.isNaN(value)) return '';
  return `${value.toFixed(fractionDigits)}%`;
}

export function referenceHref(reference: NoteReference): string | null {
  if (reference.url && /^https?:\/\//i.test(reference.url)) return reference.url;
  return null;
}

export function isServiceUnavailable(result: APIResult<unknown>): boolean {
  return result.status === 0 || result.status >= 500;
}

export function isAuthRequired(result: APIResult<unknown>): boolean {
  return result.status === 401 || result.status === 403;
}

export function isNotFound(result: APIResult<unknown>): boolean {
  return result.status === 404;
}
