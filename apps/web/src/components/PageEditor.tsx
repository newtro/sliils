import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactElement } from 'react';
import { EditorContent, useEditor } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { Collaboration } from '@tiptap/extension-collaboration';
import { CollaborationCaret } from '@tiptap/extension-collaboration-caret';
import { createYjsProvider, YSweetProvider } from '@y-sweet/client';
import * as Y from 'yjs';

import type { Page, PageAuth } from '../api/pages';
import { issuePageAuth } from '../api/pages';

// PageEditor wires TipTap to a Y-Sweet document. The Go server mints a
// short-lived auth token (POST /pages/:id/auth); we hand that to the
// @y-sweet/client helper which opens the underlying websocket on our
// behalf. Y-Sweet tokens expire on the order of 25 minutes — we refresh
// the moment we get within 2 minutes of expiry so an open editor never
// silently drops its sync.

type Props = {
  page: Page;
  me: { id: number; display_name: string };
  canWrite?: boolean;
};

// Deterministic pastel for each collaborator based on their user id.
// Nothing secret here — we just want two people to see distinct carets.
function colorForUser(id: number): string {
  const hues = [200, 280, 20, 120, 340, 50, 180, 240];
  return `hsl(${hues[id % hues.length]} 80% 55%)`;
}

export function PageEditor({ page, me, canWrite = true }: Props): ReactElement {
  const [authError, setAuthError] = useState<string | null>(null);
  const [provider, setProvider] = useState<YSweetProvider | null>(null);

  // Keep the latest auth in a ref so the authEndpoint function (passed
  // once to Y-Sweet) always returns fresh credentials when Y-Sweet
  // asks to rotate.
  const authRef = useRef<PageAuth | null>(null);

  // Initialise the provider + Y.Doc once per page. We pass a function as
  // `authEndpoint` so Y-Sweet re-calls us whenever it needs a new token
  // (startup + every periodic refresh). That's also how we survive 401s
  // from the WS server when tokens expire.
  useEffect(() => {
    let cancelled = false;
    const ydoc = new Y.Doc();

    const authEndpoint = async () => {
      const next = await issuePageAuth(page.id);
      authRef.current = next;
      return {
        url: next.url,
        baseUrl: next.base_url,
        docId: next.doc_id,
        token: next.token,
      };
    };

    // Do an initial auth up front so we know the docId (required as the
    // second arg to createYjsProvider) and so we can surface auth errors
    // before the editor even mounts.
    void (async () => {
      try {
        const firstToken = await authEndpoint();
        if (cancelled) return;
        const p = createYjsProvider(ydoc, firstToken.docId, authEndpoint, {
          initialClientToken: firstToken,
        });
        setProvider(p);
        setAuthError(null);
      } catch (err) {
        if (cancelled) return;
        setAuthError((err as Error).message);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [page.id]);

  // Wire awareness state so other clients see our display name + caret
  // colour. Safe to call on the null path — useEffect re-runs when
  // provider materialises.
  useEffect(() => {
    if (!provider) return;
    provider.awareness.setLocalStateField('user', {
      name: me.display_name || `User ${me.id}`,
      color: colorForUser(me.id),
    });
  }, [provider, me.display_name, me.id]);

  // Access the Y.Doc via the provider's public API. The provider holds
  // a reference to the doc we gave it; @y-sweet/client doesn't expose it
  // as a public field, so we read it from awareness (same underlying doc).
  const ydoc = useMemo(() => provider?.awareness.doc ?? null, [provider]);

  const editor = useEditor(
    {
      extensions:
        provider && ydoc
          ? [
              // StarterKit ships paragraph / heading / list / bold / italic /
              // code / link and more. Undo/redo is disabled when Collaboration
              // is on because Yjs's undo stack takes over.
              StarterKit.configure({ undoRedo: false }),
              Collaboration.configure({ document: ydoc }),
              CollaborationCaret.configure({
                provider,
                user: { name: me.display_name || `User ${me.id}`, color: colorForUser(me.id) },
              }),
            ]
          : [StarterKit],
      editable: canWrite,
      immediatelyRender: false,
    },
    [provider, ydoc, canWrite, me.display_name, me.id],
  );

  // Tear down the provider on unmount so the websocket closes cleanly.
  useEffect(() => {
    return () => {
      if (provider) {
        provider.destroy();
      }
    };
  }, [provider]);

  if (authError) {
    return (
      <div role="alert" style={{ padding: 16, color: '#b00' }}>
        Unable to connect to the collaboration server: {authError}
      </div>
    );
  }
  if (!editor || !provider) {
    return <div style={{ padding: 16, color: '#777' }}>Loading…</div>;
  }

  return (
    <div className="page-editor-host" style={{ border: '1px solid #eee', borderRadius: 6, minHeight: 400 }}>
      <EditorContent editor={editor} />
    </div>
  );
}
