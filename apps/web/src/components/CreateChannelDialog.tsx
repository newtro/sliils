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
      className="sl-dialog-overlay"
      role="dialog"
      aria-modal="true"
      aria-label="Create a channel"
      onClick={onClose}
    >
      <div className="sl-dialog-panel" onClick={(e) => e.stopPropagation()}>
        <header className="sl-dialog-head">
          <h2 className="sl-dialog-title">Create a channel</h2>
          <button
            type="button"
            onClick={onClose}
            className="sl-dialog-close"
            aria-label="Close"
          >
            ×
          </button>
        </header>

        <form onSubmit={submit}>
          <label style={{ display: 'block', marginBottom: 12 }}>
            <span className="sl-dialog-label">Name</span>
            <div style={{ display: 'flex', alignItems: 'stretch' }}>
              <span className="sl-dialog-hash">#</span>
              <input
                autoFocus
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. engineering"
                maxLength={80}
                className="sl-dialog-input"
                style={{ borderRadius: '0 var(--r-md) var(--r-md) 0' }}
              />
            </div>
            <div className="sl-dialog-help">
              {normalised ? (
                <>
                  Will be created as <strong>#{normalised}</strong>
                </>
              ) : (
                'Lowercase letters, digits, and hyphens.'
              )}
            </div>
          </label>

          <label style={{ display: 'block', marginBottom: 16 }}>
            <span className="sl-dialog-label">Topic (optional)</span>
            <input
              type="text"
              value={topic}
              onChange={(e) => setTopic(e.target.value)}
              placeholder="What is this channel about?"
              maxLength={250}
              className="sl-dialog-input"
            />
          </label>

          {error && (
            <div role="alert" className="sl-dialog-error">
              {error}
            </div>
          )}

          <footer className="sl-dialog-foot">
            <button type="button" onClick={onClose} className="sl-dialog-secondary">
              Cancel
            </button>
            <button
              type="submit"
              className="sl-primary sl-primary-sm"
              disabled={!normalised || createMutation.isPending}
            >
              {createMutation.isPending ? 'Creating…' : 'Create channel'}
            </button>
          </footer>
        </form>
      </div>
    </div>
  );
}
