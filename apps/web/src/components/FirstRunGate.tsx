import { useEffect, useState } from 'react';
import type { ReactElement, ReactNode } from 'react';
import { useLocation, useNavigate } from 'react-router';

import { getFirstRunState } from '../api/firstRun';

// FirstRunGate is a one-shot redirector that runs on every app load.
// If /first-run/state reports completed=false, it pushes the browser to
// /first-run so a blank install lands the operator in the wizard
// regardless of which URL they started at. When completed=true (the
// common case for an already-initialised install) it's a no-op.
//
// The wizard page itself renders the actual UI; this gate only decides
// whether the user should be sent there.

export function FirstRunGate({ children }: { children: ReactNode }): ReactElement {
  const nav = useNavigate();
  const loc = useLocation();
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getFirstRunState()
      .then((s) => {
        if (cancelled) return;
        if (!s.completed && loc.pathname !== '/first-run') {
          nav('/first-run', { replace: true });
        }
      })
      .catch(() => {
        // If the endpoint errors we don't want to block the app from
        // loading — leave the user on whatever route they opened.
      })
      .finally(() => {
        if (!cancelled) setChecked(true);
      });
    return () => {
      cancelled = true;
    };
    // Intentionally only runs once per mount. The wizard itself handles
    // post-bootstrap redirects, and any other route that needs to
    // reconsult state can do so independently.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!checked) {
    return <div className="sl-placeholder">Loading…</div>;
  }
  return <>{children}</>;
}
