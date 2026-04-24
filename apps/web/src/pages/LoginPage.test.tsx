import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { routes } from '../routes';

function renderAt(path: string): void {
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  render(<RouterProvider router={router} />);
}

describe('auth pages', () => {
  const originalFetch = globalThis.fetch;

  beforeEach(() => {
    // AuthProvider calls /auth/refresh on mount. Return 401 so the render
    // settles into the unauthenticated state for all tests below.
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 401 })) as typeof fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('renders the SliilS wordmark on the login page', async () => {
    renderAt('/login');
    expect(await screen.findByRole('heading', { name: /SliilS/i })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Sign in/i })).toBeInTheDocument();
  });

  it('has email and password fields on login', async () => {
    renderAt('/login');
    expect(await screen.findByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
  });

  it('signup page exposes display name, email, password', async () => {
    renderAt('/signup');
    expect(await screen.findByLabelText(/display name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
  });

  it('magic-link page has email field and submit', async () => {
    renderAt('/magic-link');
    expect(await screen.findByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /sign-in link/i })).toBeInTheDocument();
  });

  it('forgot-password page has email field and submit', async () => {
    renderAt('/forgot-password');
    expect(await screen.findByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /send reset link/i })).toBeInTheDocument();
  });
});
