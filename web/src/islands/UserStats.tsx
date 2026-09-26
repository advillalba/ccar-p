import { useEffect, useState, useMemo } from 'preact/hooks';
import type { Domain } from '../lib';

const difficulties = ['beginner', 'intermediate', 'advanced', 'exam_scenarios'] as const;
const practiceCookieName = 'ccarp_practice';

const getCookie = (name: string): string | null => {
  if (typeof document === 'undefined') return null;
  try {
    const m = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : null;
  } catch { return null; }
};

type Q = { id: string; domain_id?: string; difficulty: string };
type AnswerMap = Record<string, boolean>;

export default function UserStats() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [questionsByDiff, setQuestionsByDiff] = useState<Record<string, Q[]>>({});
  const [answers, setAnswers] = useState<AnswerMap>({});
  const [attempts, setAttempts] = useState<any[]>([]);
  const [isLoggedIn, setIsLoggedIn] = useState<boolean | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    (async () => {
      try {
        const fetchedDomains = await fetch('/api/v1/domains', { headers: { accept: 'application/json' } }).then(r => r.json().catch(() => null)).then((j: any) => j?.data?.domains ?? j?.domains ?? j?.data ?? []);
        if (Array.isArray(fetchedDomains)) setDomains(fetchedDomains as Domain[]);

        const qPromises = difficulties.map(d => fetch(`/api/v1/practice/questions?difficulty=${d}`, { headers: { accept: 'application/json' } }).then(r => r.json().catch(() => null)).then((j: any) => {
          const arr: any[] = j?.data?.questions ?? j?.questions ?? j?.data ?? [];
          return [d, Array.isArray(arr) ? arr : []] as const;
        }));
        const qPairs = await Promise.all(qPromises);
        const map: Record<string, Q[]> = {};
        for (const [d, arr] of qPairs) map[d] = arr as Q[];
        setQuestionsByDiff(map);

        const pRes = await fetch('/api/v1/practice/progress', { credentials: 'same-origin', headers: { accept: 'application/json' } });
        if (pRes.status === 401) {
          setIsLoggedIn(false);
          const raw = getCookie(practiceCookieName);
          let data: any = null;
          if (raw) {
            try {
              let b64 = raw.replace(/-/g, '+').replace(/_/g, '/');
              while (b64.length % 4) b64 += '=';
              data = JSON.parse(atob(b64));
            } catch { try { data = JSON.parse(atob(raw)); } catch { data = null; } }
          }
          const m: AnswerMap = {};
          if (data && typeof data === 'object') for (const [k, v] of Object.entries(data as any)) m[k] = typeof v === 'boolean' ? v : !!(v as any).is_correct;
          try {
            for (let i = 0; i < localStorage.length; i++) {
              const lk = localStorage.key(i);
              if (lk && lk.startsWith('ccarp.practice.')) {
                const parsed: any = JSON.parse(localStorage.getItem(lk) || '{}');
                for (const [k, v] of Object.entries(parsed?.results ?? {})) if (typeof v === 'boolean' && !(k in m)) m[k] = v as boolean;
              }
            }
          } catch {}
          setAnswers(m);
        } else if (pRes.ok) {
          setIsLoggedIn(true);
          const j: any = await pRes.json().catch(() => null);
          const arr: any[] = j?.data?.answers ?? j?.answers ?? [];
          const m: AnswerMap = {};
          for (const a of arr) if (a?.question_id) m[a.question_id] = !!a.is_correct;
          setAnswers(m);
        } else setIsLoggedIn(false);

        const aRes = await fetch('/api/v1/attempts', { credentials: 'same-origin', headers: { accept: 'application/json' } });
        if (aRes.ok) {
          const j: any = await aRes.json().catch(() => null);
          const arr: any[] = j?.data?.attempts ?? j?.attempts ?? j?.data ?? [];
          setAttempts(Array.isArray(arr) ? arr : []);
        }
      } catch {} finally { setLoading(false); }
    })();
  }, []);

  const allQuestions = useMemo(() => Object.values(questionsByDiff).flat(), [questionsByDiff]);

  const statsByDiff = useMemo(() => difficulties.map(d => {
    const qs = questionsByDiff[d] ?? [];
    let answered = 0, correct = 0;
    for (const q of qs) if (q.id in answers) { answered++; if (answers[q.id]) correct++; }
    return { difficulty: d, total: qs.length, answered, correct, accuracy: answered ? Math.round((correct / answered) * 100) : 0 };
  }), [questionsByDiff, answers]);

  const overall = useMemo(() => {
    let total = allQuestions.length, answered = 0, correct = 0;
    for (const q of allQuestions) if (q.id in answers) { answered++; if (answers[q.id]) correct++; }
    return { total, answered, correct, accuracy: answered ? Math.round((correct / answered) * 100) : 0 };
  }, [allQuestions, answers]);

  const byDomain = useMemo(() => {
    const domainMap = new Map<string, { domain: Domain; total: number; answered: number; correct: number }>();
    for (const d of domains) domainMap.set(d.id, { domain: d, total: 0, answered: 0, correct: 0 });
    for (const q of allQuestions) {
      const entry = domainMap.get(q.domain_id ?? '');
      if (!entry) continue;
      entry.total++;
      if (q.id in answers) { entry.answered++; if (answers[q.id]) entry.correct++; }
    }
    return Array.from(domainMap.values()).sort((a, b) => a.domain.sort_order - b.domain.sort_order || a.domain.name.localeCompare(b.domain.name));
  }, [domains, allQuestions, answers]);

  if (loading) return <div class="card"><p class="muted">Loading statistics…</p></div>;

  return (
    <div style="display:grid;gap:1.25rem">
      <div class="card">
        <h2 style="margin:0">Overall</h2>
        <p class="muted" style="margin:.25rem 0 .5rem;font-size:.9em">{isLoggedIn ? 'Progress saved to your account' : 'Progress saved in browser (sign in to keep it)'} · {overall.answered}/{overall.total} answered · {overall.correct} correct · {overall.accuracy}% accuracy</p>
        <div style="height:8px;background:var(--border);border-radius:99px;overflow:hidden">
          <div style={`height:100%;width:${overall.total ? Math.round((overall.answered/overall.total)*100) : 0}%;background:var(--accent)`} />
        </div>
        <div style="display:flex;gap:.5rem;margin-top:.75rem;flex-wrap:wrap">
          <a class="btn btn--primary" href="/practice">Go to practice</a>
          <a class="btn btn--secondary" href="/attempts">View attempts ({attempts.length})</a>
        </div>
      </div>

      <section class="card">
        <h2 style="margin:0 0 .5rem">Progress by level</h2>
        <div style="display:grid;gap:.75rem">
          {statsByDiff.map(s => (
            <div style="border:1px solid var(--border);border-radius:.6rem;padding:.75rem">
              <div style="display:flex;justify-content:space-between;align-items:center;gap:.5rem;flex-wrap:wrap">
                <strong style="text-transform:capitalize">{s.difficulty}</strong>
                <span class="badge badge--outline">{s.answered}/{s.total} · {s.accuracy}%</span>
              </div>
              <div style="height:6px;background:var(--border);border-radius:99px;overflow:hidden;margin:.5rem 0">
                <div style={`height:100%;width:${s.total ? Math.round((s.answered/s.total)*100) : 0}%;background:${s.answered===s.total && s.total>0 ? 'var(--success)' : 'var(--accent)'}`} />
              </div>
              <div style="display:flex;gap:.5rem;flex-wrap:wrap;font-size:.85em" class="muted">
                <span>{s.correct} correct</span><span>·</span><span>{s.answered - s.correct} incorrect</span>
              </div>
              <a class="btn btn--secondary" href={`/practice/${s.difficulty}`} style="margin-top:.5rem">Practice {s.difficulty}</a>
            </div>
          ))}
        </div>
      </section>

      <section class="card">
        <h2 style="margin:0 0 .5rem">Progress by domain</h2>
        {byDomain.length===0 && <p class="muted">No domains found.</p>}
        {byDomain.length>0 && (
          <div style="display:grid;gap:.6rem">
            {byDomain.map(({ domain, total, answered, correct }) => {
              const pct = total ? Math.round((answered/total)*100) : 0;
              const acc = answered ? Math.round((correct/answered)*100) : 0;
              const done = total>0 && answered===total;
              return (
                <div style={`border:1px solid ${done ? 'var(--success)' : 'var(--border)'};border-radius:.6rem;padding:.65rem .75rem;background:${done ? 'var(--success-soft)' : 'transparent'}`}>
                  <div style="display:flex;justify-content:space-between;align-items:center;gap:.5rem;flex-wrap:wrap">
                    <span><strong>{domain.name}</strong> {done && <span class="badge badge--success">✓ completed</span>}</span>
                    <span class="muted" style="font-size:.85em">{answered}/{total} · {acc}% accuracy</span>
                  </div>
                  <div style="height:6px;background:var(--border);border-radius:99px;overflow:hidden;margin:.45rem 0">
                    <div style={`height:100%;width:${pct}%;background:${done ? 'var(--success)' : 'var(--accent)'}`} />
                  </div>
                  <div class="muted" style="font-size:.82em;display:flex;gap:.5rem;flex-wrap:wrap">
                    <span>{correct} correct</span><span>·</span><span>{answered - correct} incorrect</span><span>·</span><span>{total - answered} remaining</span>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </section>

      {attempts.length>0 && (
        <section class="card">
          <h2 style="margin:0 0 .5rem">Recent exam attempts</h2>
          <div style="display:grid;gap:.4rem">
            {attempts.slice(0,5).map((a:any) => (
              <a class="list-row list-row--link" href={`/attempts/${a.id}`}>
                <div class="list-row__body"><span class="list-row__title">{a.exam_title ?? a.exam_slug}</span><span class="muted" style="font-size:.85em">{a.status} · {a.score_percentage!=null ? `${Math.round(a.score_percentage)}%` : ''}</span></div>
                <span>›</span>
              </a>
            ))}
          </div>
          <a class="btn btn--secondary" href="/attempts" style="margin-top:.6rem">View all attempts</a>
        </section>
      )}
    </div>
  );
}
