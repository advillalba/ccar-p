import { useState, useEffect } from 'preact/hooks';

const CONSENT_COOKIE = 'ccarp_cookie_consent';
const CONSENT_LS = 'ccarp_cookie_consent';

function getConsent(): string | null {
  try {
    const ls = localStorage.getItem(CONSENT_LS);
    if (ls) return ls;
  } catch {}
  try {
    const m = document.cookie.match(new RegExp('(?:^|; )' + CONSENT_COOKIE + '=([^;]*)'));
    if (m) return decodeURIComponent(m[1]);
  } catch {}
  return null;
}

function setConsent(value: 'accepted' | 'rejected') {
  try { localStorage.setItem(CONSENT_LS, value); } catch {}
  try {
    const expires = new Date(Date.now() + 365 * 864e5).toUTCString();
    document.cookie = `${CONSENT_COOKIE}=${value}; expires=${expires}; path=/; SameSite=Lax`;
  } catch {}
}

export default function CookieConsent() {
  const [consent, setConsentState] = useState<string | null>(null);
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
    setConsentState(getConsent());
  }, []);

  if (!mounted) return null;
  if (consent === 'accepted') return null;

  const accept = () => {
    setConsent('accepted');
    setConsentState('accepted');
  };

  const reject = () => {
    setConsent('rejected');
    setConsentState('rejected');
  };

  const reconsider = () => {
    try { localStorage.removeItem(CONSENT_LS); } catch {}
    try { document.cookie = `${CONSENT_COOKIE}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/`; } catch {}
    setConsentState(null);
  };

  if (consent === 'rejected') {
    return (
      <div style="position:fixed;inset:0;z-index:9999;background:var(--bg);color:var(--text);display:flex;align-items:center;justify-content:center;padding:2rem;text-align:center">
        <div style="max-width:480px;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius-l);padding:1.5rem;box-shadow:var(--shadow-2)">
          <h2 style="margin:0 0 .5rem;font-size:1.5rem;color:var(--text)">Cookies required</h2>
          <p style="margin:0 0 1rem;color:var(--text-muted)">You have rejected cookies. To use CCAR-P you must accept them. Without cookies we cannot remember your progress or keep your session.</p>
          <p style="margin:0 0 1.5rem;color:var(--text-muted);font-size:.9em">If you change your mind, you can accept below.</p>
          <div style="display:flex;gap:.75rem;justify-content:center;flex-wrap:wrap">
            <button type="button" class="btn btn--primary" onClick={reconsider}>Accept cookies and continue</button>
            <a class="btn btn--secondary" href="about:blank">Leave</a>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div role="dialog" aria-label="Cookie consent" style="position:fixed;bottom:0;left:0;right:0;z-index:9998;background:color-mix(in srgb, var(--surface) 94%, transparent);color:var(--text);padding:1rem 1.25rem;backdrop-filter:blur(10px);-webkit-backdrop-filter:blur(10px);border-top:1px solid var(--border);display:flex;gap:1rem;align-items:center;flex-wrap:wrap;justify-content:space-between;box-shadow:var(--shadow-2)">
      <div style="flex:1;min-width:260px">
        <p style="margin:0;font-weight:600;color:var(--text)">We use cookies</p>
        <p style="margin:.25rem 0 0;color:var(--text-muted);font-size:.9em;line-height:1.4">We use cookies to remember your practice progress, keep your session and improve the experience. If you reject, you will be signed out of the site.</p>
      </div>
      <div style="display:flex;gap:.5rem;flex-shrink:0">
        <button type="button" class="btn btn--secondary" onClick={reject}>Reject</button>
        <button type="button" class="btn btn--primary" onClick={accept}>Accept</button>
      </div>
    </div>
  );
}
