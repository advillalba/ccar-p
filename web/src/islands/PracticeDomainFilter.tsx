import { useState, useEffect } from 'preact/hooks';
import type { Domain } from '../lib';

interface Props {
  domains: Domain[];
  selected: string[];
  difficulty: string;
}

const practiceCookieName = 'ccarp_practice';

const getCookie = (name: string): string | null => {
  if (typeof document === 'undefined') return null;
  try {
    const m = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : null;
  } catch { return null; }
};

export default function PracticeDomainFilter({ domains, selected: initial, difficulty }: Props) {
  const [selected, setSelected] = useState<string[]>(initial);
  const [open, setOpen] = useState(false);
  const [completed, setCompleted] = useState<Set<string>>(new Set());

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [qRes, pRes] = await Promise.all([
          fetch(`/api/v1/practice/questions?difficulty=${encodeURIComponent(difficulty)}`, { credentials: 'same-origin', headers: { accept: 'application/json' } }),
          fetch('/api/v1/practice/progress', { credentials: 'same-origin', headers: { accept: 'application/json' } }),
        ]);
        const qPayload: any = await qRes.json().catch(() => null);
        const qData: any[] = (qPayload?.data?.questions ?? qPayload?.questions ?? qPayload?.data ?? []) as any;
        const allQs: any[] = Array.isArray(qData) ? qData : [];
        // group by domain
        const byDomain = new Map<string, number>();
        for (const q of allQs) {
          const did = q.domain_id || q.domainId || '';
          if (!did) continue;
          byDomain.set(did, (byDomain.get(did) || 0) + 1);
        }
        let answeredSet = new Set<string>();
        if (pRes.status === 401) {
          const raw = getCookie(practiceCookieName);
          if (raw) {
            try {
              let b64 = raw.replace(/-/g, '+').replace(/_/g, '/');
              while (b64.length % 4) b64 += '=';
              const data: any = JSON.parse(atob(b64));
              if (data && typeof data === 'object') for (const k of Object.keys(data)) answeredSet.add(k);
            } catch {
              try { const d: any = JSON.parse(atob(raw)); for (const k of Object.keys(d)) answeredSet.add(k); } catch {}
            }
          }
          try {
            for (let i = 0; i < localStorage.length; i++) {
              const lk = localStorage.key(i);
              if (lk && lk.startsWith('ccarp.practice.')) {
                const parsed: any = JSON.parse(localStorage.getItem(lk) || '{}');
                const r = parsed?.results;
                if (r) for (const k of Object.keys(r)) answeredSet.add(k);
              }
            }
          } catch {}
        } else if (pRes.ok) {
          const pPayload: any = await pRes.json().catch(() => null);
          const answers: any[] = pPayload?.data?.answers ?? pPayload?.answers ?? [];
          for (const a of answers) if (a?.question_id) answeredSet.add(a.question_id);
        }
        // count answered per domain
        const answeredByDomain = new Map<string, number>();
        for (const q of allQs) {
          if (answeredSet.has(q.id)) {
            const did = q.domain_id || q.domainId || '';
            answeredByDomain.set(did, (answeredByDomain.get(did) || 0) + 1);
          }
        }
        const comp = new Set<string>();
        for (const [did, total] of byDomain) {
          if ((answeredByDomain.get(did) || 0) >= total && total > 0) comp.add(did);
        }
        if (!cancelled) setCompleted(comp);
      } catch {}
    })();
    return () => { cancelled = true; };
  }, [difficulty]);

  const toggle = (id: string) => {
    setSelected(cur => cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id]);
  };

  const apply = () => {
    const params = new URLSearchParams();
    if (selected.length) params.set('domains', selected.join(','));
    const qs = params.toString();
    window.location.assign(`/practice/${encodeURIComponent(difficulty)}${qs ? `?${qs}` : ''}`);
  };

  const clear = () => {
    setSelected([]);
    window.location.assign(`/practice/${encodeURIComponent(difficulty)}`);
  };

  if (domains.length === 0) return null;

  const hasChanges = JSON.stringify([...selected].sort()) !== JSON.stringify([...initial].sort());

  return (
    <section class="card" style="margin-bottom:1rem">
      <button type="button" onClick={() => setOpen(v => !v)} aria-expanded={open} style="width:100%;display:flex;justify-content:space-between;align-items:center;background:none;border:none;padding:0;cursor:pointer;text-align:left">
        <h2 style="margin:0;font-size:1rem">Filter by domain</h2>
        <span aria-hidden="true" style="font-size:1.25rem">{open ? '−' : '+'}</span>
      </button>
      {!open && initial.length>0 && <p class="muted" style="margin:.35rem 0 0;font-size:.85em">{initial.length} domain{initial.length===1?'':'s'} selected — expand to edit.</p>}
      {!open && initial.length===0 && <p class="muted" style="margin:.35rem 0 0;font-size:.85em">All domains (tap to filter) {completed.size>0 && <span class="badge badge--success" style="margin-left:.5rem">{completed.size} completed</span>}</p>}
      {open && (
        <>
          <p class="muted" style="margin:.5rem 0 .75rem">Only questions from the selected domains will be shown. Leave empty for all. <span style="color:var(--success)">Green = completed for this level.</span></p>
          <div style="display:flex;flex-wrap:wrap;gap:.5rem;margin-bottom:.75rem">
            {domains.sort((a,b)=>a.sort_order-b.sort_order||a.name.localeCompare(b.name)).map(d=>{
              const checked = selected.includes(d.id);
              const isCompleted = completed.has(d.id);
              const style = checked
                ? 'background:var(--accent-soft);border-color:var(--accent);color:var(--accent)'
                : isCompleted
                ? 'background:var(--success-soft);border-color:var(--success);color:var(--success)'
                : '';
              return (
                <label key={d.id} class="badge" style={`cursor:pointer;display:inline-flex;align-items:center;gap:.35rem;border:1px solid var(--border);padding:.35rem .6rem;${style}`}>
                  <input type="checkbox" checked={checked} onInput={()=>toggle(d.id)} /> {d.name} {isCompleted && '✓'}
                </label>
              );
            })}
          </div>
          <div style="display:flex;gap:.5rem;flex-wrap:wrap">
            <button type="button" class="btn btn--primary" onClick={apply} disabled={!hasChanges}>Apply filter</button>
            {initial.length>0 && <button type="button" class="btn btn--secondary" onClick={clear}>Clear filter</button>}
            <a class="btn btn--secondary" href="/practice">Change difficulty</a>
          </div>
          {initial.length>0 && <p class="muted" style="margin:.5rem 0 0;font-size:.85em">Currently filtering by {initial.length} domain{initial.length===1?'':'s'}.</p>}
        </>
      )}
    </section>
  );
}
