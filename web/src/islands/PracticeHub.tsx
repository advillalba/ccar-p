import { useState, useEffect } from 'preact/hooks';
import type { Domain } from '../lib';

interface Level { slug: string; label: string; pitch: string; badge: string; }

interface Props {
  domains: Domain[];
  levels: Level[];
}

const practiceCookieName = 'ccarp_practice';

const getCookie = (name: string): string | null => {
  if (typeof document === 'undefined') return null;
  try {
    const m = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
    return m ? decodeURIComponent(m[1]) : null;
  } catch { return null; }
};

export default function PracticeHub({ domains, levels }: Props) {
  const [selected, setSelected] = useState<string[]>([]);
  const [open, setOpen] = useState(false);
  const [completedDomains, setCompletedDomains] = useState<Set<string>>(new Set());
  const [completedLevels, setCompletedLevels] = useState<Set<string>>(new Set());

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [qRes, pRes] = await Promise.all([
          fetch('/api/v1/practice/questions', { credentials: 'same-origin', headers: { accept: 'application/json' } }),
          fetch('/api/v1/practice/progress', { credentials: 'same-origin', headers: { accept: 'application/json' } }),
        ]);
        const qPayload: any = await qRes.json().catch(() => null);
        const qData: any[] = (qPayload?.data?.questions ?? qPayload?.questions ?? qPayload?.data ?? []) as any;
        const allQs: any[] = Array.isArray(qData) ? qData : [];
        const byDomain = new Map<string, number>();
        const byLevel = new Map<string, number>();
        for (const q of allQs) {
          const did = q.domain_id || '';
          const lvl = q.difficulty || '';
          if (did) byDomain.set(did, (byDomain.get(did) || 0) + 1);
          if (lvl) byLevel.set(lvl, (byLevel.get(lvl) || 0) + 1);
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
        const answeredByDomain = new Map<string, number>();
        const answeredByLevel = new Map<string, number>();
        for (const q of allQs) {
          if (answeredSet.has(q.id)) {
            const did = q.domain_id || '';
            const lvl = q.difficulty || '';
            if (did) answeredByDomain.set(did, (answeredByDomain.get(did) || 0) + 1);
            if (lvl) answeredByLevel.set(lvl, (answeredByLevel.get(lvl) || 0) + 1);
          }
        }
        const compDomains = new Set<string>();
        for (const [did, total] of byDomain) if ((answeredByDomain.get(did) || 0) >= total && total > 0) compDomains.add(did);
        const compLevels = new Set<string>();
        for (const [lvl, total] of byLevel) if ((answeredByLevel.get(lvl) || 0) >= total && total > 0) compLevels.add(lvl);
        if (!cancelled) {
          setCompletedDomains(compDomains);
          setCompletedLevels(compLevels);
        }
      } catch {}
    })();
    return () => { cancelled = true; };
  }, []);

  const toggle = (id: string) => {
    setSelected(current => current.includes(id) ? current.filter(x => x !== id) : [...current, id]);
  };

  const domainsParam = selected.length > 0 ? `?domains=${selected.map(encodeURIComponent).join(',')}` : '';
  const selectAll = () => setSelected(domains.map(d => d.id));
  const clearAll = () => setSelected([]);

  return (
    <div>
      {domains.length > 0 && (
        <section class="card" style="margin-bottom:1.5rem">
          <button type="button" onClick={() => setOpen(v => !v)} aria-expanded={open} style="width:100%;display:flex;justify-content:space-between;align-items:center;background:none;border:none;padding:0;cursor:pointer;text-align:left">
            <h2 style="margin:0;font-size:1rem">Filter by domain</h2>
            <span aria-hidden="true" style="font-size:1.25rem">{open ? '−' : '+'}</span>
          </button>
          {!open && <p class="muted" style="margin:.35rem 0 0;font-size:.85em">{selected.length ? `${selected.length} domain${selected.length===1?'':'s'} selected — tap to edit` : 'All domains — tap to filter'} {completedDomains.size>0 && <span class="badge badge--success" style="margin-left:.5rem">{completedDomains.size} completed</span>}</p>}
          {open && (
            <>
              <p class="muted" style="margin:.5rem 0 .75rem">Select one or more domains to practice only those questions. Leave empty to include all domains. <span style="color:var(--success)">Green = completed.</span></p>
              <div style="display:flex;gap:.5rem;margin-bottom:.75rem;flex-wrap:wrap">
                <button type="button" class="btn btn--secondary" onClick={selectAll} disabled={selected.length===domains.length}>Select all</button>
                <button type="button" class="btn btn--secondary" onClick={clearAll} disabled={selected.length===0}>Clear</button>
                <span class="muted" style="align-self:center">{selected.length ? `${selected.length} selected` : 'All domains'}</span>
              </div>
              <div style="display:flex;flex-wrap:wrap;gap:.5rem">
                {domains.sort((a,b)=>a.sort_order-b.sort_order||a.name.localeCompare(b.name)).map(domain=> {
                  const checked = selected.includes(domain.id);
                  const isCompleted = completedDomains.has(domain.id);
                  const style = checked
                    ? 'background:var(--accent-soft);border-color:var(--accent);color:var(--accent)'
                    : isCompleted
                    ? 'background:var(--success-soft);border-color:var(--success);color:var(--success)'
                    : '';
                  return (
                    <label key={domain.id} class="badge" style={`cursor:pointer;display:inline-flex;align-items:center;gap:.35rem;border:1px solid var(--border);padding:.35rem .6rem;${style}`}>
                      <input type="checkbox" checked={checked} onInput={()=>toggle(domain.id)} />
                      {domain.name} {isCompleted && '✓'}
                    </label>
                  );
                })}
              </div>
            </>
          )}
        </section>
      )}

      <nav class="list" aria-label="Practice difficulties">
        {levels.map(level => {
          const isCompleted = completedLevels.has(level.slug);
          return (
            <a class="list-row card--interactive" href={`/practice/${level.slug}${domainsParam}`} style={isCompleted ? 'border-color:var(--success);background:var(--success-soft)' : ''}>
              <div class="list-row__body">
                <h2 class="card__title" style={isCompleted ? 'color:var(--success)' : ''}>{level.label} {isCompleted && '✓'}</h2>
                <div class="muted">{level.pitch}</div>
                <span class={`badge ${isCompleted ? 'badge--success' : level.badge}`}>{isCompleted ? 'Completed' : level.label}</span>
                {selected.length>0 && <div class="muted" style="margin-top:.25rem;font-size:.85em">{selected.length} domain{selected.length===1?'':'s'} selected</div>}
              </div>
              <span class="list-row__chev" aria-hidden="true">&rsaquo;</span>
            </a>
          );
        })}
      </nav>
      {selected.length>0 && <p class="muted" style="margin-top:.75rem">You will practice only questions from the selected domains.</p>}
    </div>
  );
}
