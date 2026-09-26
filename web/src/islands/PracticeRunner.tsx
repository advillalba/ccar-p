import { useState, useEffect, useMemo, useRef } from 'preact/hooks';
import type { PracticeFeedback, PracticeQuestion } from '../lib';
import { correctAnswerLine, toggleSelection } from './answers';
import './practice-runner.css';

type PracticeDifficulty = 'beginner' | 'intermediate' | 'advanced' | 'exam_scenarios';

interface PracticeRunnerProps {
  difficulty: PracticeDifficulty;
  questions: PracticeQuestion[];
  domains?: string[];
  // 'api' (default, SSR): grading + progress via the backend.
  // 'local' (static site): grading in the browser against the options'
  // is_correct flags; progress is cookie + localStorage only.
  gradingMode?: 'api' | 'local';
  // Domain id -> name; enables the domain filter in local grading mode.
  domainNames?: Record<string, string>;
  // Deployment base path for the "Back to practice" links (static site only).
  basePath?: string;
}

const practiceCookieName = 'ccarp_practice';

const getCookie = (name: string): string | null => {
  if (typeof document === 'undefined') return null;
  try {
    const match = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
    return match ? decodeURIComponent(match[1]) : null;
  } catch { return null; }
};

const setCookie = (name: string, value: string, days = 180) => {
  if (typeof document === 'undefined') return;
  try {
    const expires = new Date(Date.now() + days * 864e5).toUTCString();
    document.cookie = `${name}=${encodeURIComponent(value)}; expires=${expires}; path=/; SameSite=Lax`;
  } catch {}
};

const deleteCookie = (name: string) => {
  if (typeof document === 'undefined') return;
  try { document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/; SameSite=Lax`; } catch {}
};

const encodePracticeCookie = (data: Record<string, any>): string => {
  if (typeof btoa === 'undefined') return '';
  try {
    const b64 = btoa(unescape(encodeURIComponent(JSON.stringify(data))));
    return b64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/,'');
  } catch { return ''; }
};

const decodePracticeCookie = (value: string): Record<string, any> | null => {
  if (typeof atob === 'undefined') return null;
  try {
    const json = decodeURIComponent(escape(atob(value)));
    const parsed = JSON.parse(json);
    if (typeof parsed === 'object' && parsed !== null) return parsed as Record<string, any>;
    return null;
  } catch {
    try {
      const b = atob(value);
      const parsed = JSON.parse(b);
      if (typeof parsed === 'object' && parsed !== null) return parsed;
    } catch {}
    return null;
  }
};

const decodeLegacyBase64 = (value: string): Record<string, any> | null => {
  try {
    const b = atob(value);
    const parsed = JSON.parse(b);
    if (typeof parsed === 'object' && parsed !== null) return parsed;
    return null;
  } catch { return null; }
};

const readCookieResults = (questionIds: Set<string>): Record<string, boolean> => {
  const raw = getCookie(practiceCookieName);
  if (!raw) {
    // try legacy base64 cookie set by backend (RawURLEncoding without padding)
    const altRaw = getCookie(practiceCookieName);
    if (!altRaw) return {};
  }
  let data: Record<string, any> | null = null;
  if (raw) {
    data = decodePracticeCookie(raw) ?? decodeLegacyBase64(raw);
    if (!data) {
      // try RawURLEncoding variant (backend uses base64.RawURLEncoding)
      try {
        let padded = raw.replace(/-/g, '+').replace(/_/g, '/');
        while (padded.length % 4) padded += '=';
        const json = atob(padded);
        data = JSON.parse(json);
      } catch { data = null; }
    }
  }
  if (!data || typeof data !== 'object') return {};
  const out: Record<string, boolean> = {};
  for (const [qid, val] of Object.entries(data)) {
    if (!questionIds.has(qid)) continue;
    if (typeof val === 'boolean') out[qid] = val;
    else if (val && typeof val === 'object' && 'is_correct' in val && typeof (val as any).is_correct === 'boolean') out[qid] = (val as any).is_correct;
  }
  return out;
};

const writeCookieResults = (current: Record<string, boolean>, updates: Record<string, boolean>) => {
  const existingRaw = getCookie(practiceCookieName);
  let existing: Record<string, any> = {};
  if (existingRaw) {
    existing = decodePracticeCookie(existingRaw) ?? decodeLegacyBase64(existingRaw) ?? {};
    if (!existing || typeof existing !== 'object') existing = {};
    // try RawURLEncoding fallback
    if (Object.keys(existing).length === 0) {
      try {
        let padded = existingRaw.replace(/-/g, '+').replace(/_/g, '/');
        while (padded.length % 4) padded += '=';
        existing = JSON.parse(atob(padded)) ?? {};
      } catch { existing = {}; }
    }
  }
  for (const [k, v] of Object.entries(updates)) {
    existing[k] = { is_correct: v, selected: [] };
  }
  // also merge current (in case updates is empty, we want to persist current)
  for (const [k, v] of Object.entries(current)) {
    if (!(k in existing)) existing[k] = { is_correct: v, selected: [] };
  }
  setCookie(practiceCookieName, encodePracticeCookie(existing));
};

const domainsKey = (domains?: string[]): string => {
  if (!domains || domains.length === 0) return 'all';
  return [...domains].sort().join(',');
};

const storageKey = (difficulty: PracticeDifficulty, domains?: string[]): string => `ccarp.practice.${difficulty}.${domainsKey(domains)}.v2`;
const orderKey = (difficulty: PracticeDifficulty, domains?: string[]): string => `ccarp.practice.${difficulty}.${domainsKey(domains)}.order.v2`;

const shuffleArray = <T,>(array: T[]): T[] => {
  const out = [...array];
  for (let i = out.length - 1; i > 0; i--) {
    const r = typeof crypto !== 'undefined' && (crypto as any).getRandomValues
      ? (() => { const a = new Uint32Array(1); (crypto as any).getRandomValues(a); return a[0] % (i + 1); })()
      : Math.floor(Math.random() * (i + 1));
    [out[i], out[r]] = [out[r], out[i]];
  }
  return out;
};

const readShuffledOrder = (difficulty: PracticeDifficulty, domains: string[] | undefined): string[] | null => {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(orderKey(difficulty, domains));
    if (!raw) return null;
    const parsed: any = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed as string[];
    if (parsed && Array.isArray(parsed.order)) return parsed.order as string[];
    return null;
  } catch { return null; }
};

const writeShuffledOrder = (difficulty: PracticeDifficulty, domains: string[] | undefined, order: string[]) => {
  if (typeof window === 'undefined') return;
  try { window.localStorage.setItem(orderKey(difficulty, domains), JSON.stringify(order)); } catch {}
};

const readLocalStorageResults = (difficulty: PracticeDifficulty, domains: string[] | undefined, questionIds: Set<string>): Record<string, boolean> => {
  if (typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(storageKey(difficulty, domains));
    if (!raw) return {};
    const parsed: any = JSON.parse(raw);
    const stored = parsed?.results;
    if (typeof stored !== 'object' || stored === null) return {};
    const out: Record<string, boolean> = {};
    for (const [id, value] of Object.entries(stored)) {
      if (questionIds.has(id) && typeof value === 'boolean') out[id] = value;
    }
    return out;
  } catch { return {}; }
};

const errorFromPayload = (payload: unknown): string => {
  if (payload && typeof payload === 'object' && 'error' in payload) {
    const error = (payload as { error?: unknown }).error;
    if (error && typeof error === 'object' && 'message' in error) {
      const message = (error as { message?: unknown }).message;
      if (typeof message === 'string' && message.length > 0) return message;
    }
  }
  return 'The request could not be completed.';
};

const feedbackFromPayload = (payload: unknown): PracticeFeedback | null => {
  if (!payload || typeof payload !== 'object') return null;
  const data = (payload as { data?: PracticeFeedback | null }).data;
  const feedback = (data ?? payload) as PracticeFeedback | null;
  if (!feedback || typeof feedback !== 'object') return null;
  if (typeof feedback.is_correct !== 'boolean' || !Array.isArray(feedback.options)) return null;
  return feedback;
};

// Local grading (static site): compare the selection against the options
// marked correct in the public dump. Mirrors the backend practice endpoint's
// feedback shape so the runner UI behaves identically.
const feedbackFromQuestion = (question: PracticeQuestion, selectedOptionIds: string[]): PracticeFeedback => {
  const correctOptionIds = question.options.filter(option => option.is_correct).map(option => option.id ?? option.key);
  const selectedSorted = [...selectedOptionIds].sort();
  const correctSorted = [...correctOptionIds].sort();
  const isCorrect = selectedSorted.length === correctSorted.length && selectedSorted.every((id, index) => id === correctSorted[index]);
  return {
    id: question.id,
    position: question.position,
    domain_id: question.domain_id,
    prompt: question.prompt,
    scenario: question.scenario,
    difficulty: question.difficulty,
    explanation: question.explanation ?? '',
    options: question.options.map(option => ({
      id: option.id ?? option.key,
      key: option.key,
      text: option.text,
      explanation: option.explanation ?? '',
      position: option.position
    })),
    references: [],
    selected_option_ids: selectedOptionIds,
    correct_option_ids: correctOptionIds,
    is_correct: isCorrect
  };
};

const computeShuffled = (questions: PracticeQuestion[], difficulty: PracticeDifficulty, domains?: string[]): PracticeQuestion[] => {
  if (typeof window === 'undefined') return questions;
  if (questions.length <= 1) return questions;
  const ids = questions.map(q => q.id);
  const stored = readShuffledOrder(difficulty, domains);
  if (stored && stored.length === ids.length && stored.every(id => ids.includes(id))) {
    const byId = new Map(questions.map(q => [q.id, q] as const));
    return stored.map(id => byId.get(id)!).filter(Boolean) as PracticeQuestion[];
  }
  if (stored && stored.length > 0) {
    const storedSet = new Set(stored);
    const missing = ids.filter(id => !storedSet.has(id));
    if (missing.length > 0) {
      const byId = new Map(questions.map(q => [q.id, q] as const));
      const base = stored.map(id => byId.get(id)!).filter(Boolean) as PracticeQuestion[];
      const extra = shuffleArray(missing.map(id => byId.get(id)!).filter(Boolean) as PracticeQuestion[]);
      const merged = [...base, ...extra];
      writeShuffledOrder(difficulty, domains, merged.map(q => q.id));
      return merged;
    }
  }
  const shuffled = shuffleArray(questions);
  writeShuffledOrder(difficulty, domains, shuffled.map(q => q.id));
  return shuffled;
};

export default function PracticeRunner({ difficulty, questions, domains, gradingMode = 'api', domainNames = {}, basePath = '' }: PracticeRunnerProps) {
  // Static site (gradingMode: 'local'): client-side domain filter. SSR keeps
  // the prop-driven `domains` (an empty selection falls back to it below).
  const [selectedDomainIds, setSelectedDomainIds] = useState<string[]>([]);
  // In local grading mode the question list is the full difficulty, so the
  // domain selection filters it here; in SSR mode the API already filtered.
  const questionPool = useMemo(
    () => (gradingMode === 'local' && selectedDomainIds.length > 0
      ? questions.filter(question => question.domain_id !== undefined && selectedDomainIds.includes(question.domain_id))
      : questions),
    [gradingMode, selectedDomainIds, questions]
  );
  const [shuffledQuestions, setShuffledQuestions] = useState<PracticeQuestion[]>(() => computeShuffled(questionPool, difficulty, domains));
  const previousPool = useRef(questionPool);
  const effectiveDomains = useMemo(
    () => (selectedDomainIds.length > 0 ? selectedDomainIds : domains),
    [selectedDomainIds, domains]
  );
  const availableDomainIds = useMemo(
    () => Object.keys(domainNames).filter(id => questions.some(question => question.domain_id === id)),
    [domainNames, questions]
  );

  const questionIds = useMemo(() => new Set(shuffledQuestions.map(q => q.id)), [shuffledQuestions]);
  // Initialize empty so SSR and client first render match (hydration-safe);
  // the progress-load effect below restores results from DB/cookie/localStorage.
  const [results, setResults] = useState<Record<string, boolean>>({});
  const [isLoggedIn, setIsLoggedIn] = useState<boolean | null>(null);
  const [currentIdx, setCurrentIdx] = useState<number>(0);
  const [selected, setSelected] = useState<string[]>([]);
  const [feedback, setFeedback] = useState<PracticeFeedback | null>(null);
  const [grading, setGrading] = useState(false);
  const [error, setError] = useState('');
  const [resetting, setResetting] = useState(false);
  const [feedbacks, setFeedbacks] = useState<Record<string, PracticeFeedback>>({});
  const [showMdx, setShowMdx] = useState(false);
  const [copied, setCopied] = useState(false);
  const [filterOpen, setFilterOpen] = useState(false);

  const key = storageKey(difficulty, effectiveDomains);

  // Load progress from DB if logged in, otherwise from cookie + localStorage.
  // In local grading mode (static site) there is no backend: restore from
  // cookie + localStorage only.
  useEffect(() => {
    if (gradingMode === 'local') {
      const cookieRes = readCookieResults(questionIds);
      const localRes = readLocalStorageResults(difficulty, effectiveDomains, questionIds);
      const merged = { ...localRes, ...cookieRes };
      setIsLoggedIn(false);
      setResults(merged);
      setCurrentIdx(shuffledQuestions.findIndex(q => !(q.id in merged)));
      return;
    }
    let cancelled = false;
    const restoreLocal = () => {
      const cookieRes = readCookieResults(questionIds);
      const localRes = readLocalStorageResults(difficulty, effectiveDomains, questionIds);
      const merged = { ...localRes, ...cookieRes };
      setResults(merged);
      setCurrentIdx(shuffledQuestions.findIndex(q => !(q.id in merged)));
    };
    (async () => {
      try {
        const res = await fetch('/api/v1/practice/progress', { credentials: 'same-origin', headers: { accept: 'application/json' } });
        if (res.status === 401) {
          if (!cancelled) {
            setIsLoggedIn(false);
            restoreLocal();
          }
          return;
        }
        if (!res.ok) { if (!cancelled) { setIsLoggedIn(false); restoreLocal(); } return; }
        const payload: any = await res.json().catch(() => null);
        const data = payload?.data ?? payload;
        const answers: any[] = data?.answers ?? [];
        const mapped: Record<string, boolean> = {};
        for (const a of answers) {
          if (a?.question_id && typeof a.is_correct === 'boolean' && questionIds.has(a.question_id)) mapped[a.question_id] = a.is_correct;
        }
        if (!cancelled) {
          setIsLoggedIn(true);
          setResults(mapped);
          setCurrentIdx(shuffledQuestions.findIndex(q => !(q.id in mapped)));
        }
      } catch {
        if (!cancelled) {
          setIsLoggedIn(false);
          restoreLocal();
        }
      }
    })();
    return () => { cancelled = true; };
  }, [gradingMode, questionIds, difficulty, effectiveDomains, shuffledQuestions]);

  useEffect(() => {
    try {
      window.localStorage.setItem(key, JSON.stringify({ results }));
    } catch {}
    if (isLoggedIn === false) {
      writeCookieResults(results, {});
    }
  }, [key, results, isLoggedIn]);

  const stats = useMemo(() => {
    let answered = 0;
    let correct = 0;
    for (const question of shuffledQuestions) {
      if (results[question.id] === true) { answered++; correct++; }
      else if (question.id in results) answered++;
    }
    return { answered, correct, accuracy: answered === 0 ? 0 : Math.round((correct / answered) * 100) };
  }, [shuffledQuestions, results]);

  if (questions.length === 0) return null;

  const question = currentIdx >= 0 ? shuffledQuestions[currentIdx] : undefined;
  const correctLine = feedback ? correctAnswerLine(feedback.options, feedback.correct_option_ids ?? []) : '';

  const selectOption = (id: string) => {
    setSelected(current => toggleSelection(current, id));
  };

  const checkAnswer = async (event: Event) => {
    event.preventDefault();
    if (!question || selected.length === 0 || grading) return;
    setGrading(true);
    setError('');
    if (gradingMode === 'local') {
      // Static site: grade in the browser, no API round-trip. Progress still
      // lands in localStorage + the practice cookie (isLoggedIn === false).
      const graded = feedbackFromQuestion(question, selected);
      const next = { ...results, [question.id]: graded.is_correct };
      setIsLoggedIn(false);
      setResults(next);
      setFeedbacks(prev => ({ ...prev, [question.id]: graded }));
      writeCookieResults(next, { [question.id]: graded.is_correct });
      setFeedback(graded);
      setGrading(false);
      return;
    }
    try {
      const response = await fetch(`/api/v1/exams/${encodeURIComponent(question.exam_slug ?? question.id)}/practice`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ question_id: question.id, selected_option_ids: selected })
      });
      const payload: unknown = await response.json().catch(() => null);
      if (!response.ok) throw new Error(errorFromPayload(payload));
      const graded = feedbackFromPayload(payload);
      if (!graded) throw new Error('The answer response is incomplete.');
      const next = { ...results, [question.id]: graded.is_correct };
      setResults(next);
      setFeedbacks(prev => ({ ...prev, [question.id]: graded }));
      if (isLoggedIn === false) {
        writeCookieResults(next, { [question.id]: graded.is_correct });
      }
      setFeedback(graded);
    } catch (caught) {
      setError(caught instanceof Error && caught.message ? caught.message : 'The request could not be completed.');
    } finally {
      setGrading(false);
    }
  };

  // When the question pool changes (domain filter), reshuffle it under the new
  // selection key and restart the queue. Skipped on mount so the initial
  // shuffle and the progress-restore effect above stay authoritative.
  useEffect(() => {
    if (previousPool.current === questionPool) return;
    previousPool.current = questionPool;
    const next = computeShuffled(questionPool, difficulty, effectiveDomains);
    setShuffledQuestions(next);
    setSelected([]);
    setFeedback(null);
    setError('');
    setCurrentIdx(next.length > 0 ? next.findIndex(q => !(q.id in results)) : -1);
  }, [questionPool]);

  const advance = () => {
    setSelected([]);
    setFeedback(null);
    setError('');
    setCurrentIdx(shuffledQuestions.findIndex(item => !(item.id in results)));
  };

  const reshuffle = () => {
    const shuffled = shuffleArray(shuffledQuestions);
    writeShuffledOrder(difficulty, effectiveDomains, shuffled.map(q => q.id));
    setShuffledQuestions(shuffled);
    setCurrentIdx(shuffled.findIndex(q => !(q.id in results)));
    setFeedback(null);
    setSelected([]);
    setError('');
  };

  const startAgain = () => {
    try { window.localStorage.removeItem(key); } catch {}
    const shuffled = shuffleArray(shuffledQuestions);
    writeShuffledOrder(difficulty, effectiveDomains, shuffled.map(q => q.id));
    setShuffledQuestions(shuffled);
    setResults({});
    setSelected([]);
    setFeedback(null);
    setError('');
    setCurrentIdx(0);
  };

  const toggleDomain = (id: string) => {
    setSelectedDomainIds(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id]);
  };

  const resetProgress = async () => {
    if (resetting) return;
    setResetting(true);
    try {
      // Clear DB if logged in
      if (isLoggedIn) {
        await fetch('/api/v1/practice/progress', { method: 'DELETE', credentials: 'same-origin' });
      }
      // Clear cookie and localStorage
      deleteCookie(practiceCookieName);
      try {
        for (let i = 0; i < localStorage.length; i++) {
          const k = localStorage.key(i);
          if (k && k.startsWith('ccarp.practice.')) { localStorage.removeItem(k); i--; }
        }
      } catch {}
      // Also clear current key
      try { window.localStorage.removeItem(key); } catch {}
      setResults({});
      setFeedbacks({});
      setSelected([]);
      setFeedback(null);
      setError('');
      setCurrentIdx(0);
    } finally {
      setResetting(false);
    }
  };

  const generateMdx = (): string => {
    const date = new Date().toLocaleDateString('en-US');
    const total = shuffledQuestions.length;
    const correctQs = shuffledQuestions.filter(q => results[q.id] === true);
    const failedQs = shuffledQuestions.filter(q => results[q.id] === false);
    const pendingQs = shuffledQuestions.filter(q => !(q.id in results));
    let mdx = `---\ntitle: "CCAR-P Practice Report"\ndate: "${date}"\ndifficulty: "${difficulty}"\ndomains: "${domainsKey(effectiveDomains)}"\ncorrect: ${stats.correct}\nanswered: ${stats.answered}\ntotal: ${total}\naccuracy: ${stats.accuracy}\n---\n\n`;
    mdx += `# Performance report — Practice ${difficulty}\n\n`;
    mdx += `**Date:** ${date}  \n`;
    mdx += `**Difficulty:** ${difficulty}  \n`;
    mdx += `**Domains:** ${effectiveDomains && effectiveDomains.length ? effectiveDomains.join(', ') : 'all'}  \n`;
    mdx += `**Result:** ${stats.correct}/${total} correct · ${stats.answered} answered · ${stats.accuracy}% accuracy\n\n`;
    mdx += `## Summary\n\n`;
    mdx += `- ✅ Correct: ${correctQs.length}\n`;
    mdx += `- ❌ Incorrect: ${failedQs.length}\n`;
    mdx += `- ⏳ Pending: ${pendingQs.length}\n\n`;
    const fmtOpts = (opts: any[]) => opts.map(o => `${o.key}. ${o.text}`).join(' | ');
    const fmtFeedback = (q: PracticeQuestion) => {
      const fb = feedbacks[q.id];
      const correctKeys = fb ? (fb.correct_option_ids ?? []).map(id => fb.options.find(o => o.id === id)?.key ?? id).join(', ') : '—';
      const selectedKeys = fb ? (fb.selected_option_ids ?? []).map(id => fb.options.find(o => o.id === id)?.key ?? id).join(', ') : '—';
      return { correctKeys, selectedKeys, fb };
    };
    if (correctQs.length) {
      mdx += `## ✅ Correct (${correctQs.length})\n\n`;
      correctQs.forEach((q, idx) => {
        const { correctKeys, selectedKeys, fb } = fmtFeedback(q);
        mdx += `### ${idx + 1}. ${q.prompt}\n\n`;
        if (q.scenario) mdx += `> ${q.scenario}\n\n`;
        mdx += `**Options:** ${fmtOpts(q.options)}  \n`;
        if (fb) {
          mdx += `**Your answer:** ${selectedKeys} — ✅ Correct  \n`;
          mdx += `**Correct:** ${correctKeys}  \n`;
          if (fb.explanation) mdx += `**Explanation:** ${fb.explanation}  \n`;
        } else {
          mdx += `**Status:** Correct  \n`;
        }
        mdx += `\n`;
      });
    }
    if (failedQs.length) {
      mdx += `## ❌ Incorrect (${failedQs.length})\n\n`;
      failedQs.forEach((q, idx) => {
        const { correctKeys, selectedKeys, fb } = fmtFeedback(q);
        mdx += `### ${idx + 1}. ${q.prompt}\n\n`;
        if (q.scenario) mdx += `> ${q.scenario}\n\n`;
        mdx += `**Options:** ${fmtOpts(q.options)}  \n`;
        if (fb) {
          mdx += `**Your answer:** ${selectedKeys} — ❌  \n`;
          mdx += `**Correct:** ${correctKeys}  \n`;
          // highlight partial correct: show which were correct and also selected
          const selectedSet = new Set(fb.selected_option_ids ?? []);
          const correctSet = new Set(fb.correct_option_ids ?? []);
          const partial = [...selectedSet].filter(id => correctSet.has(id));
          if (partial.length) {
            const partialKeys = partial.map(id => fb.options.find(o => o.id === id)?.key ?? id).join(', ');
            mdx += `**Partially correct:** ${partialKeys} (stays green)  \n`;
          }
          if (fb.explanation) mdx += `**Explanation:** ${fb.explanation}  \n`;
        } else {
          mdx += `**Status:** Incorrect — Correct: ${correctKeys}  \n`;
        }
        mdx += `\n`;
      });
    }
    if (pendingQs.length) {
      mdx += `## ⏳ Pending (${pendingQs.length})\n\n`;
      pendingQs.forEach((q, idx) => {
        mdx += `### ${idx + 1}. ${q.prompt}\n\n`;
        if (q.scenario) mdx += `> ${q.scenario}\n\n`;
        mdx += `**Options:** ${fmtOpts(q.options)}\n\n`;
      });
    }
    mdx += `---\n*Generated by CCAR-P — ${date}*\n`;
    return mdx;
  };

  const mdxReport = generateMdx();
  const copyMdx = async () => {
    try {
      await navigator.clipboard.writeText(mdxReport);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // fallback
      try {
        const ta = document.createElement('textarea');
        ta.value = mdxReport;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        ta.remove();
        setCopied(true);
        setTimeout(() => setCopied(false), 1800);
      } catch {}
    }
  };

  return (
    <div class="practice-runner">
      {Object.keys(domainNames).length > 0 && (
        <section class="card" style="margin-bottom:1rem">
          <button type="button" onClick={() => setFilterOpen(v => !v)} aria-expanded={filterOpen} style="width:100%;display:flex;justify-content:space-between;align-items:center;background:none;border:none;padding:0;cursor:pointer;text-align:left">
            <h2 style="margin:0;font-size:1rem">Filter by domain</h2>
            <span aria-hidden="true" style="font-size:1.25rem">{filterOpen ? '−' : '+'}</span>
          </button>
          {!filterOpen && <p class="muted" style="margin:.35rem 0 0;font-size:.85em">{selectedDomainIds.length ? `${selectedDomainIds.length} domain${selectedDomainIds.length===1?'':'s'} selected — tap to edit` : 'All domains — tap to filter'}</p>}
          {filterOpen && (
            <>
              <p class="muted" style="margin:.5rem 0 .75rem">Select one or more domains to practice only those questions. Leave empty to include all domains.</p>
              <div style="display:flex;gap:.5rem;margin-bottom:.75rem;flex-wrap:wrap">
                <button type="button" class="btn btn--secondary" onClick={() => setSelectedDomainIds(availableDomainIds)} disabled={selectedDomainIds.length === availableDomainIds.length}>Select all</button>
                <button type="button" class="btn btn--secondary" onClick={() => setSelectedDomainIds([])} disabled={selectedDomainIds.length === 0}>Clear</button>
                <span class="muted" style="align-self:center">{selectedDomainIds.length ? `${selectedDomainIds.length} selected` : 'All domains'}</span>
              </div>
              <div style="display:flex;flex-wrap:wrap;gap:.5rem">
                {availableDomainIds.map(id => {
                  const checked = selectedDomainIds.includes(id);
                  const style = checked ? 'background:var(--accent-soft);border-color:var(--accent);color:var(--accent)' : '';
                  return (
                    <label key={id} class="badge" style={`cursor:pointer;display:inline-flex;align-items:center;gap:.35rem;border:1px solid var(--border);padding:.35rem .6rem;${style}`}>
                      <input type="checkbox" checked={checked} onInput={() => toggleDomain(id)} />
                      {domainNames[id]}
                    </label>
                  );
                })}
              </div>
            </>
          )}
        </section>
      )}
      <div class="practice-runner__progress">
        <div
          class="progress"
          role="progressbar"
          aria-label="Practice progress"
          aria-valuemin={0}
          aria-valuemax={shuffledQuestions.length}
          aria-valuenow={stats.answered}
        >
          <div
            class={`progress__fill${stats.answered === shuffledQuestions.length ? ' progress__fill--success' : ''}`}
            style={{ width: `${Math.round((stats.answered / shuffledQuestions.length) * 100)}%` }}
          />
        </div>
        <p class="progress__meta">
          <span>{stats.answered} of {shuffledQuestions.length} practiced</span>
          {stats.answered > 0 && <span>{stats.correct} correct ({stats.accuracy}%)</span>}
        </p>
        <div style="display:flex;gap:.5rem;margin-top:.5rem;flex-wrap:wrap">
          <button type="button" class="btn btn--secondary" onClick={reshuffle} title="Shuffle the filtered questions (difficulty + selected categories)">
            Shuffle
          </button>
          <button type="button" class="btn btn--secondary" onClick={resetProgress} disabled={resetting} title="Clear all practice progress (cookie and database)">
            {resetting ? 'Resetting…' : 'Reset progress'}
          </button>
          <button type="button" class="btn btn--secondary" onClick={() => setShowMdx(v => !v)} title="View the MDX report to copy">
            {showMdx ? 'Hide report' : 'View MDX report'}
          </button>
          <a class="btn btn--secondary" href={`${basePath}/review`}>Review mistakes</a>
          {stats.answered>0 && <span class="muted" style="align-self:center;font-size:.85em">{isLoggedIn ? 'Saved to your account' : 'Saved in cookie'}</span>}
        </div>
        <p class="muted" style="margin:.35rem 0 0;font-size:.8em">Randomized within your selection: {difficulty}{effectiveDomains && effectiveDomains.length ? ` + ${effectiveDomains.length} categories` : ' · all categories'} — {shuffledQuestions.length} questions.</p>
      </div>
      {showMdx && (
        <div class="card" style="margin-top:1rem">
          <div style="display:flex;justify-content:space-between;align-items:center;gap:.5rem;flex-wrap:wrap">
            <h3 style="margin:0">MDX report</h3>
            <button type="button" class="btn btn--primary" onClick={copyMdx}>{copied ? 'Copied!' : 'Copy MDX'}</button>
          </div>
          <p class="muted" style="margin:.5rem 0;font-size:.85em">Copy this report in MDX format with correct and incorrect answers. Partially correct answers stay green.</p>
          <pre style="white-space:pre-wrap;word-break:break-word;background:var(--surface-2);border:1px solid var(--border);color:var(--text);padding:.75rem;border-radius:8px;max-height:420px;overflow:auto;font-size:.85em;line-height:1.5">{mdxReport}</pre>
        </div>
      )}
      {question && !feedback && (
        <form class="practice-runner__form" onSubmit={checkAnswer}>
          <fieldset class="practice-runner__fieldset">
            <legend class="visually-hidden">Choose one option</legend>
            <h2 class="prompt">{question.prompt}</h2>
            {question.scenario && <p class="scenario">{question.scenario}</p>}
            <ol class="options">
              {question.options.map(option => {
                const value = option.id ?? option.key;
                return (
                  <li key={value}>
                    <label class="opt">
                      <input type="checkbox" name={`q-${question.id}`} value={value} checked={selected.includes(value)} onInput={() => selectOption(value)} />
                      <span class="opt__key" aria-hidden="true">{option.key}.</span>
                      <span>{option.text}</span>
                    </label>
                  </li>
                );
              })}
            </ol>
          </fieldset>
          {error && (
            <div class="alert alert--error" role="alert">
              <p><strong>We could not check that answer.</strong> {error} Your choice is still selected; press Check answer to retry.</p>
            </div>
          )}
          <div class="btnrow">
            <button type="submit" class="btn btn--primary" disabled={selected.length === 0 || grading} aria-busy={grading || undefined}>
              {grading ? 'Checking…' : 'Check answer'}
            </button>
          </div>
        </form>
      )}
      {question && feedback && (
        <div class="practice-runner__graded">
          <h2 class="prompt">{question.prompt}</h2>
          {question.scenario && <p class="scenario">{question.scenario}</p>}
          <div class="practice-runner__feedback" role="status" aria-live="polite">
            <p class={`verdict ${feedback.is_correct ? 'verdict--correct' : 'verdict--incorrect'}`}>{feedback.is_correct ? 'Correct' : 'Incorrect'}</p>
            {correctLine && <p>{correctLine}</p>}
            <div class="options">
              {feedback.options.map(option => {
                const isCorrectOption = (feedback.correct_option_ids ?? []).includes(option.id);
                const isSelectedOption = (feedback.selected_option_ids ?? []).includes(option.id);
                const isWrongSelection = isSelectedOption && !isCorrectOption;
                return (
                  <div key={option.id} class={`opt opt--locked${isCorrectOption ? ' opt--correct' : ''}${isWrongSelection ? ' opt--incorrect' : ''}`}>
                    <span class="opt__key" aria-hidden="true">{option.key}.</span>
                    <div class="practice-runner__option-body">
                      <span>{option.text}</span>
                      {option.explanation && <p class="opt__explanation">{option.explanation}</p>}
                    </div>
                  </div>
                );
              })}
            </div>
            {feedback.explanation && (
              <div class="callout">
                <strong>Explanation.</strong> {feedback.explanation}
              </div>
            )}
            {feedback.references?.length ? (
              <div class="callout callout--muted">
                <strong>References.</strong>
                <ul>
                  {feedback.references.map(reference => (
                    <li key={reference.position}>
                      <span>{reference.title}</span>
                      {reference.url && <a href={reference.url} rel="noopener noreferrer"> {reference.url}</a>}
                      {reference.citation && <span> &mdash; {reference.citation}</span>}
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
          </div>
          <div class="btnrow">
            <button type="button" class="btn btn--primary" onClick={advance}>Next question</button>
          </div>
        </div>
      )}
      {!question && (
        <div class="card practice-runner__done">
          <h2>All {shuffledQuestions.length} practiced</h2>
          <p class="practice-runner__score num">{stats.correct} of {shuffledQuestions.length} correct ({stats.accuracy}%)</p>
          <div class="btnrow">
            <button type="button" class="btn btn--secondary" onClick={startAgain}>Start again</button>
            <a class="btn btn--secondary" href={`${basePath}/practice`}>Back to practice</a>
            <button type="button" class="btn btn--secondary" onClick={resetProgress} disabled={resetting}>{resetting ? 'Resetting…' : 'Reset progress'}</button>
            <button type="button" class="btn btn--secondary" onClick={() => setShowMdx(v => !v)}>{showMdx ? 'Hide report' : 'View MDX report'}</button>
          </div>
        </div>
      )}
    </div>
  );
}
