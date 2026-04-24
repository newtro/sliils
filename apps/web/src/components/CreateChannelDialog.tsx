import { useEffect, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';

import { ApiError } from '../api/client';
import { createWorkspaceChannel } from '../api/workspaces';

interface Props {
  workspaceSlug: string;
  open: boolean;
  onClose: () => void;
  onCreated?: (channelID: number) => void;
}

export function CreateChannelDialog({
  workspaceSlug,
  open,
  onClose,
  onCreated,
}: Props): ReactElement | null {
  const qc = useQueryClient();
  const [name, setName] = useState('');
  const [topic, setTopic] = useState('');
  const [error, setError] = useState<string | null>(null);

  // Normalise the name to the same ruleset the server enforces
  // (lowercase, a-z0-9-), so users see the preview match what lands.
  const normalised = name
    .toLowerCase()
    .replace(/\s+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-+/g, '-');

  useEffect(() => {
    if (!open) {
      setName('');
      setTopic('');
      setError(null);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  const createMutation = useMutation({
    mutationFn: () =>
      createWorkspaceChannel(workspaceSlug, {
        name: normalised,
        topic: topic.trim() || undefined,
      }),
    onSuccess: (ch) => {
      qc.invalidateQueries({ queryKey: ['workspace', workspaceSlug, 'channels'] });
      onCreated?.(ch.id);
      onClose();
    },
    onError: (err: Error) => {
      if (err instanceof ApiError) {
        setError(err.problem.detail ?? err.message);
      } else {
        setError(err.message);
      }
    },
  });

  if (!open) return null;

  function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!normalised) {
      setError('Name is required');
      return;
    }
    createMutation.mutate();
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Create a channel"
      style={overlayStyle}
      onClick={onClose}
    >
      <div style={panelStyle} onClick={(e) => e.stopPropagation()}>
        <header style={headerStyle}>
          <h2 style={{ margin: 0, fontSize: 18 }}>Create a channel</h2>
          <button type="button" onClick={onClose} style={closeBtn} aria-label="Close">
            ×
          </button>
        </header>

        <form onSubmit={submit}>
          <label style={{ display: 'block', marginBottom: 12 }}>
            <span style={labelStyle}>Name</span>
            <div style={{ display: 'flex', alignItems: 'center' }}>
              <span style={hashStyle}>#</span>
              <input
                autoFocus
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. engineering"
                maxLength={80}
                style={inputStyle}
              />
            </div>
            <div style={helpStyle}>
              {normalised
                ? <>Will be created as <strong>#{normalised}</strong></>
                : 'Lowercase letters, digits, and hyphens.'}
            </div>
          </label>

          <label style={{ display: 'block', marginBottom: 16 }}>
            <span style={labelStyle}>Topic (optional)</span>
            <input
              type="text"
              value={topic}
              onChange={(e) => setTopic(e.target.value)}
              placeholder="What is this channel about?"
              maxLength={250}
              style={inputStyle}
            />
          </label>

          {error && <div role="alert" style={errorStyle}>{error}</div>}

          <footer style={footerStyle}>
            <button type="button" onClick={onClose} style={secondaryBtn}>
              Cancel
            </button>
            <button
              type="submit"
              disabled={!normalised || createMutation.isPending}
              style={primaryBtn}
            >
              {createMutation.isPending ? 'Creating…' : 'Create channel'}
            </button>
          </footer>
        </form>
      </div>
    </div>
  );
}

// ---- styles (inline for self-containment) ------------------------------

const overlayStyle: React.CSSProperties = {
  position: 'fixed',
  inset: 0,
  background: 'rgba(0,0,0,0.4)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  zIndex: 1000,
};

const panelStyle: React.CSSProperties = {
  width: 440,
  background: 'var(--surface, #fff)',
  color: 'var(--text, #1a1a1a)',
  borderRadius: 8,
  padding: 20,
  boxShadow: '0 10px 40px rgba(0,0,0,0.3)',
};

const headerStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
  marginBottom: 16,
};

const labelStyle: React.CSSProperties = {
  display: 'block',
  fontSize: 12,
  color: 'var(--text-muted, #666)',
  marginBottom: 4,
};

const hashStyle: React.CSSProperties = {
  padding: '8px 10px',
  color: 'var(--text-subtle, #999)',
  background: 'var(--surface-raised, #f6f6f6)',
  border: '1px solid var(--border, #ddd)',
  borderRight: 'none',
  borderRadius: '4px 0 0 4px',
  fontWeight: 500,
};

const inputStyle: React.CSSProperties = {
  width: '100%',
  padding: '8px 10px',
  border: '1px solid var(--border, #ddd)',
  borderRadius: 4,
  background: 'var(--surface, #fff)',
  color: 'inherit',
  fontFamily: 'inherit',
  fontSize: 14,
  boxSizing: 'border-box',
};

const helpStyle: React.CSSProperties = {
  marginTop: 4,
  fontSize: 12,
  color: 'var(--text-muted, #777)',
};

const errorStyle: React.CSSProperties = {
  padding: 8,
  background: 'rgba(200,40,40,0.08)',
  color: '#a33',
  borderRadius: 4,
  marginBottom: 12,
  fontSize: 13,
};

const footerStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'flex-end',
  gap: 8,
  marginTop: 4,
};

const primaryBtn: React.CSSProperties = {
  padding: '8px 16px',
  border: '1px solid #2a4ea4',
  background: '#2a4ea4',
  color: '#fff',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 13,
};

const secondaryBtn: React.CSSProperties = {
  padding: '8px 16px',
  border: '1px solid var(--border, #ddd)',
  background: 'transparent',
  color: 'inherit',
  borderRadius: 4,
  cursor: 'pointer',
  fontSize: 13,
};

const closeBtn: React.CSSProperties = {
  width: 28,
  height: 28,
  background: 'transparent',
  border: 'none',
  fontSize: 20,
  cursor: 'pointer',
  color: 'var(--text-muted, #888)',
};
