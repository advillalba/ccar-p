import { useState, useEffect, useMemo } from 'preact/hooks';
import type { PracticeQuestion } from '../lib';

type Filter = 'all' | 'correct' | 'incorrect';

const practiceCookieName = 'ccarp_practice';

const getCookie = (name: string): string | null => {
  if (typeof document === 'undefined') return null;
  try {
    const m = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : null;
  } catch { return null; }
};

export default function ReviewPractice() {
  const [questions, setQuestions] = useState<PracticeQuestion[]>([]);
  const [results, setResults] = useState<Record<string, boolean>>({});
  const [feedbacks, setFeedbacks] = useState<Record<string, any>>({});
  const [filter, setFilter] = useState<Filter>('all');
  const [search, setSearch] = useState('');
  const [loading, setLoading] = useState(true);
  const [isLoggedIn, setIsLoggedIn] = useState<boolean | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const [qRes, pRes] = await Promise.all([
          fetch('/api/v1/practice/questions', { credentials: 'same-origin', headers: { accept: 'application/json' } }),
          fetch('/api/v1/practice/progress', { credentials: 'same-origin', headers: { accept: 'application/json' } }),
        ]);
        const qPayload: any = await qRes.json().catch(() => null);
        const qData: PracticeQuestion[] = (qPayload?.data?.questions ?? qPayload?.questions ?? qPayload?.data ?? []) as any;
        const allQs: PracticeQuestion[] = Array.isArray(qData) ? qData : [];
        setQuestions(allQs);

        if (pRes.status === 401) {
          setIsLoggedIn(false);
          const raw = getCookie(practiceCookieName);
          let data: any = null;
          if (raw) {
            try {
              let b64 = raw.replace(/-/g, '+').replace(/_/g, '/');
              while (b64.length % 4) b64 += '=';
              data = JSON.parse(atob(b64));
            } catch {
              try { data = JSON.parse(atob(raw)); } catch { data = null; }
            }
            if (!data) {
              try { data = JSON.parse(decodeURIComponent(escape(atob(raw)))); } catch {}
            }
          }
          const map: Record<string, boolean> = {};
          const fb: Record<string, any> = {};
          if (data && typeof data === 'object') {
            for (const [k, v] of Object.entries(data as any)) {
              if (typeof v === 'boolean') map[k] = v;
              else if (v && typeof v === 'object' && 'is_correct' in v) { map[k] = (v as any).is_correct; fb[k] = v; }
            }
          }
          // also check localStorage legacy
          try {
            for (let i = 0; i < localStorage.length; i++) {
              const lk = localStorage.key(i);
              if (lk && lk.startsWith('ccarp.practice.')) {
                const parsed: any = JSON.parse(localStorage.getItem(lk) || '{}');
                const r = parsed?.results;
                if (r && typeof r === 'object') for (const [k, v] of Object.entries(r)) if (typeof v === 'boolean' && !(k in map)) map[k] = v as boolean;
              }
            }
          } catch {}
          setResults(map);
          setFeedbacks(fb);
        } else if (pRes.ok) {
          setIsLoggedIn(true);
          const pPayload: any = await pRes.json().catch(() => null);
          const answers: any[] = pPayload?.data?.answers ?? pPayload?.answers ?? [];
          const map: Record<string, boolean> = {};
          const fb: Record<string, any> = {};
          for (const a of answers) if (a?.question_id) { map[a.question_id] = !!a.is_correct; fb[a.question_id] = a; }
          setResults(map);
          setFeedbacks(fb);
        } else {
          setIsLoggedIn(false);
        }
      } catch {}
      setLoading(false);
    })();
  }, []);

  const answered = useMemo(() => questions.filter(q => q.id in results), [questions, results]);
  const filtered = useMemo(() => {
    let list = answered;
    if (filter === 'correct') list = list.filter(q => results[q.id] === true);
    if (filter === 'incorrect') list = list.filter(q => results[q.id] === false);
    if (search.trim()) {
      const s = search.trim().toLowerCase();
      list = list.filter(q => (`${q.prompt} ${q.scenario ?? ''} ${q.exam_slug}`.toLowerCase().includes(s)));
    }
    return list;
  }, [answered, filter, search, results]);

  const stats = useMemo(() => {
    let correct = 0;
    for (const q of answered) if (results[q.id]) correct++;
    return { total: questions.length, answered: answered.length, correct, incorrect: answered.length - correct, accuracy: answered.length ? Math.round((correct / answered.length) * 100) : 0 };
  }, [answered, questions, results]);

  if (loading) return <div class="card"><p class="muted">Loading your history…</p></div>;

  return (
    <div style="display:grid;gap:1rem">
      <div class="card">
        <div style="display:flex;justify-content:space-between;align-items:center;gap:.5rem;flex-wrap:wrap">
          <h2 style="margin:0">Practice history</h2>
          <span class="badge badge--outline">{stats.answered}/{stats.total} answered · {stats.accuracy}%</span>
        </div>
        <p class="muted" style="margin:.5rem 0 0;font-size:.9em">{isLoggedIn ? 'Saved in database' : 'Saved in cookie'} — filter to review mistakes only.</p>
        <div style="display:flex;gap:.5rem;margin-top:.75rem;flex-wrap:wrap">
          <input type="search" placeholder="Search prompts…" value={search} onInput={e => setSearch((e.target as HTMLInputElement).value)} style="flex:1;min-width:180px" />
          <div style="display:flex;gap:.35rem">
            {(['all','incorrect','correct'] as Filter[]).map(f => (
              <button type="button" class={`chip${filter===f?' chip--active':''}`} aria-pressed={filter===f} onClick={() => setFilter(f)}>
                {f==='all' ? `All (${stats.answered})` : f==='incorrect' ? `Incorrect (${stats.incorrect})` : `Correct (${stats.correct})`}
              </button>
            ))}
          </div>
        </div>
        {filter==='incorrect' && stats.incorrect>0 && <p class="muted" style="margin:.5rem 0 0;font-size:.85em">Showing incorrect only — ideal for review.</p>}
      </div>

      {answered.length===0 && <div class="empty"><h2>No answers yet</h2><p>Answer some questions in <a href="/practice">practice</a> and they will appear here.</p></div>}

      {filtered.length===0 && answered.length>0 && <div class="empty"><p>No questions match the current filter.</p></div>}

      <div style="display:grid;gap:.7rem">
        {filtered.map(q => {
          const isCorrect = results[q.id];
          const fb: any = feedbacks[q.id];
          return (
            <article class="card" key={q.id}>
              <div style="display:flex;justify-content:space-between;gap:.5rem;align-items:start">
                <span class={`badge ${isCorrect ? 'badge--success' : 'badge--danger'}`}>{isCorrect ? 'Correct' : 'Incorrect'}</span>
                <span class="muted" style="font-size:.8em">{q.difficulty} · {q.exam_slug}</span>
              </div>
              {q.scenario && <p class="scenario" style="margin:.5rem 0 0">{q.scenario}</p>}
              <h3 class="prompt" style="margin:.5rem 0">{q.prompt}</h3>
              <ol class="options" style="margin-top:.5rem">
                {q.options.map(o => {
                  const isSel = fb?.selected_option_ids?.includes(o.id);
                  const isCor = fb?.correct_option_ids?.includes(o.id);
                  // fallback: if we only have boolean, highlight correct via isCorrect
                  return <li key={o.id}><span class={`opt opt--locked${isCor ? ' opt--correct' : ''}${isSel && !isCor ? ' opt--incorrect' : ''}`} style="padding:.5rem .75rem"><span class="opt__key">{o.key}.</span><span>{o.text}</span>{isSel && <span class="badge badge--outline" style="margin-left:auto">You</span>}</span></li>;
                })}
              </ol>
              {fb?.explanation && <div class="callout" style="margin-top:.5rem"><strong>Explanation:</strong> {fb.explanation}</div>}
              <div style="margin-top:.5rem;display:flex;gap:.5rem">
                <a class="btn btn--secondary" href={`/practice/${q.difficulty}?domains=${q.domain_id ?? ''}`}>Practice again (filtered)</a>
              </div>
            </article>
          );
        })}
      </div>
    </div>
  );
}
