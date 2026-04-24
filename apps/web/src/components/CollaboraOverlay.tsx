import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';

import { createEditSession } from '../api/collabora';
import type { EditSession } from '../api/collabora';

type Props = {
  fileID: number;
  filename: string;
  onClose: () => void;
};

// Full-screen overlay hosting a Collabora iframe. We fetch the edit
// session up front; the iframe points at Collabora's HTML entrypoint
// which in turn calls back into our WOPI endpoints using the access
// token we baked into the URL.
//
// Auto-close on error so the user isn't trapped behind a broken iframe.
export function CollaboraOverlay({ fileID, filename, onClose }: Props): ReactElement {
  const [session, setSession] = useState<EditSession | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const s = await createEditSession(fileID);
        if (cancelled) return;
        setSession(s);
      } catch (err) {
        if (cancelled) return;
        setError((err as Error).message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [fileID]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={`Editing ${filename}`}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.4)',
        zIndex: 1000,
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '8px 16px',
          background: '#fff',
          borderBottom: '1px solid #eee',
        }}
      >
        <div style={{ fontWeight: 600 }}>{filename}</div>
        <button
          type="button"
          onClick={onClose}
          style={{ padding: '6px 12px', border: '1px solid #ddd', background: '#fff', borderRadius: 4, cursor: 'pointer' }}
        >
          Close
        </button>
      </div>
      <div style={{ flex: 1, background: '#fff' }}>
        {error && (
          <div role="alert" style={{ padding: 24, color: '#b00' }}>
            Could not open editor: {error}
          </div>
        )}
        {!error && !session && <div style={{ padding: 24, color: '#666' }}>Loading editor…</div>}
        {!error && session && (
          <iframe
            title={`Collabora editing ${filename}`}
            src={session.edit_url}
            style={{ width: '100%', height: '100%', border: 'none' }}
            // Collabora needs permissive sandboxing because it runs
            // its own service worker + cross-origin XHR. Lock down
            // further once the integration stabilises.
            allow="clipboard-read; clipboard-write"
          />
        )}
      </div>
    </div>
  );
}
