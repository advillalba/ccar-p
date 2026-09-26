import { useState } from 'preact/hooks';

const getCsrf = (): string => {
  try { return sessionStorage.getItem('ccarp-session-csrf') || ''; } catch { return ''; }
};

export default function DeleteAccount() {
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);

  const doDelete = async () => {
    setBusy(true);
    setError('');
    try {
      const csrf = getCsrf();
      // Try to get fresh session csrf via /api/v1/me if missing
      let token = csrf;
      if (!token) {
        try {
          const meRes = await fetch('/api/v1/me', { credentials: 'same-origin', headers: { accept: 'application/json' } });
          if (meRes.ok) {
            const data: any = await meRes.json().catch(() => null);
            token = data?.data?.csrf_token || '';
          }
        } catch {}
      }
      const res = await fetch('/api/v1/me', {
        method: 'DELETE',
        credentials: 'same-origin',
        headers: { 'accept': 'application/json', 'x-csrf-token': token },
      });
      if (!res.ok) {
        const payload: any = await res.json().catch(() => null);
        const msg = payload?.error?.message || 'Could not delete the account.';
        throw new Error(msg);
      }
      // Clear local data
      try {
        for (let i = 0; i < localStorage.length; i++) {
          const k = localStorage.key(i);
          if (k && (k.startsWith('ccarp.') || k === 'ccarp_cookie_consent')) { localStorage.removeItem(k); i--; }
        }
        sessionStorage.removeItem('ccarp-session-csrf');
      } catch {}
      try { document.cookie = 'ccarp_practice=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/'; } catch {}
      try { document.cookie = 'ccarp_cookie_consent=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/'; } catch {}
      setDone(true);
      setTimeout(() => { window.location.href = '/'; }, 800);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unexpected error');
      setBusy(false);
    }
  };

  if (done) {
    return (
      <div class="alert alert--success" role="status">
        <p><strong>Account deleted.</strong> Redirecting…</p>
      </div>
    );
  }

  return (
    <div class="card" style="border-color:var(--danger)">
      <h2 style="margin:0 0 .5rem;color:var(--danger)">Danger zone</h2>
      {!confirm ? (
        <button type="button" class="btn btn--secondary" style="border-color:var(--danger);color:var(--danger)" onClick={() => setConfirm(true)}>Delete my account</button>
      ) : (
        <div>
          <p style="margin:0 0 .75rem;font-weight:600">Are you sure? Type DELETE to confirm.</p>
          <div style="display:flex;gap:.5rem;flex-wrap:wrap">
            <button type="button" class="btn btn--danger" disabled={busy} onClick={doDelete}>{busy ? 'Deleting…' : 'Yes, delete my account'}</button>
            <button type="button" class="btn btn--secondary" disabled={busy} onClick={() => setConfirm(false)}>Cancel</button>
          </div>
          {error && <div class="alert alert--error" style="margin-top:.75rem" role="alert"><p>{error}</p></div>}
          <p class="muted" style="margin:.5rem 0 0;font-size:.85em">You will be signed out and all traces will be removed.</p>
        </div>
      )}
    </div>
  );
}
