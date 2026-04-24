import { useEffect, useState } from 'react';
import type { FormEvent, ReactElement } from 'react';
import { Navigate, useNavigate } from 'react-router';
import { AuthCard } from '../components/AuthCard';
import { useAuth } from '../auth/AuthContext';
import { ApiError } from '../api/client';
import { createWorkspace } from '../api/workspaces';

// Derive a URL-safe slug from a workspace name: lowercase, digits, hyphens.
function slugify(input: string): string {
  return input
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
    .slice(0, 40);
}

export function SetupPage(): ReactElement {
  const { user, loading, refreshMe } = useAuth();
  const nav = useNavigate();
  const [name, setName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugTouched, setSlugTouched] = useState(false);
  const [description, setDescription] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Keep slug in sync with name until the user edits it manually.
  useEffect(() => {
    if (!slugTouched) setSlug(slugify(name));
  }, [name, slugTouched]);

  if (loading) {
    return <AuthCard heading="Loading…" />;
  }
  if (!user) {
    return <Navigate to="/login" replace />;
  }
  // This screen handles both first-time setup AND "I want another
  // workspace." Copy adjusts; the form is identical.
  const isFirstTime = user.needs_setup;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const ws = await createWorkspace({ name, slug, description });
      await refreshMe();
      nav(`/w/${ws.slug}`, { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? (err.problem.detail ?? err.message) : 'Something went wrong');
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthCard
      heading={isFirstTime ? 'Create your workspace' : 'Create a new workspace'}
      subtext={
        isFirstTime
          ? 'One workspace per team. You can create more later.'
          : 'You can belong to as many workspaces as you like.'
      }
    >
      <form onSubmit={onSubmit} className="sl-form">
        <label className="sl-field">
          <span>Workspace name</span>
          <input
            type="text"
            required
            maxLength={64}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Acme Inc"
            autoFocus
          />
        </label>
        <label className="sl-field">
          <span>URL slug</span>
          <input
            type="text"
            required
            pattern="[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?"
            value={slug}
            onChange={(e) => {
              setSlugTouched(true);
              setSlug(e.target.value);
            }}
            placeholder="acme"
          />
          <span className="sl-hint">
            2–40 characters, lowercase letters / numbers / hyphens. Used in URLs like{' '}
            <code>/w/{slug || 'your-slug'}</code>.
          </span>
        </label>
        <label className="sl-field">
          <span>Short description</span>
          <input
            type="text"
            maxLength={240}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Shipping the future of group collaboration"
          />
        </label>
        {error && (
          <div role="alert" className="sl-error">
            {error}
          </div>
        )}
        <button type="submit" className="sl-primary" disabled={busy || !name || !slug}>
          {busy ? 'Creating…' : 'Create workspace'}
        </button>
      </form>
    </AuthCard>
  );
}
