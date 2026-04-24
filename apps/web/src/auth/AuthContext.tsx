import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactElement, ReactNode } from 'react';
import { apiFetch, configureAccessToken, tryRefresh } from '../api/client';
import { installFilesTokenGetter } from '../api/files';
import { setRealtimeToken } from '../realtime/useRealtime';

export interface User {
  id: number;
  email: string;
  display_name: string;
  email_verified_at?: string;
  created_at: string;
  needs_setup: boolean;
}

export interface SessionResponse {
  access_token: string;
  token_type: 'Bearer';
  expires_at: string;
  user: User;
}

export interface AuthContextValue {
  user: User | null;
  loading: boolean;
  refreshMe: () => Promise<User | null>;
  loginWithPassword: (email: string, password: string) => Promise<User>;
  signup: (email: string, password: string, displayName: string, inviteToken?: string) => Promise<User>;
  requestMagicLink: (email: string) => Promise<void>;
  consumeMagicLink: (token: string) => Promise<User>;
  requestPasswordReset: (email: string) => Promise<void>;
  confirmPasswordReset: (token: string, newPassword: string) => Promise<void>;
  verifyEmail: (token: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>');
  return ctx;
}

export function AuthProvider({ children }: { children: ReactNode }): ReactElement {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const tokenRef = useRef<string | null>(null);

  const setToken = useCallback((tok: string | null) => {
    tokenRef.current = tok;
    setRealtimeToken(tok);
  }, []);

  useEffect(() => {
    configureAccessToken({ get: () => tokenRef.current, set: setToken });
    installFilesTokenGetter(() => tokenRef.current);
  }, [setToken]);

  // On mount, try to re-hydrate the session via the refresh cookie.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const ok = await tryRefresh();
      if (cancelled) return;
      if (!ok) {
        setLoading(false);
        return;
      }
      try {
        const me = await apiFetch<User>('/me');
        if (!cancelled) setUser(me);
      } catch {
        if (!cancelled) setUser(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const applySession = useCallback((resp: SessionResponse): User => {
    tokenRef.current = resp.access_token;
    setUser(resp.user);
    return resp.user;
  }, []);

  // refreshMe re-reads /me (e.g. after creating a workspace) so needs_setup
  // and verification state reflect the latest server view.
  const refreshMe = useCallback(async (): Promise<User | null> => {
    try {
      const me = await apiFetch<User>('/me');
      setUser(me);
      return me;
    } catch {
      return null;
    }
  }, []);

  const loginWithPassword = useCallback(
    async (email: string, password: string): Promise<User> => {
      const resp = await apiFetch<SessionResponse>('/auth/login', {
        method: 'POST',
        body: { email, password },
        skipRefresh: true,
      });
      return applySession(resp);
    },
    [applySession],
  );

  const signup = useCallback(
    async (
      email: string,
      password: string,
      displayName: string,
      inviteToken?: string,
    ): Promise<User> => {
      const body: Record<string, string> = {
        email,
        password,
        display_name: displayName,
      };
      if (inviteToken) body.invite_token = inviteToken;
      const resp = await apiFetch<SessionResponse>('/auth/signup', {
        method: 'POST',
        body,
        skipRefresh: true,
      });
      return applySession(resp);
    },
    [applySession],
  );

  const requestMagicLink = useCallback(async (email: string) => {
    await apiFetch<{ message: string }>('/auth/magic-link/request', {
      method: 'POST',
      body: { email },
      skipRefresh: true,
    });
  }, []);

  const consumeMagicLink = useCallback(
    async (token: string): Promise<User> => {
      const resp = await apiFetch<SessionResponse>('/auth/magic-link/consume', {
        method: 'POST',
        body: { token },
        skipRefresh: true,
      });
      return applySession(resp);
    },
    [applySession],
  );

  const requestPasswordReset = useCallback(async (email: string) => {
    await apiFetch<{ message: string }>('/auth/password-reset/request', {
      method: 'POST',
      body: { email },
      skipRefresh: true,
    });
  }, []);

  const confirmPasswordReset = useCallback(async (token: string, newPassword: string) => {
    await apiFetch<{ message: string }>('/auth/password-reset/confirm', {
      method: 'POST',
      body: { token, new_password: newPassword },
      skipRefresh: true,
    });
  }, []);

  const verifyEmail = useCallback(async (token: string) => {
    await apiFetch<{ message: string }>('/auth/verify-email/consume', {
      method: 'POST',
      body: { token },
      skipRefresh: true,
    });
  }, []);

  const logout = useCallback(async () => {
    try {
      await apiFetch<{ message: string }>('/auth/logout', {
        method: 'POST',
        skipRefresh: true,
      });
    } finally {
      tokenRef.current = null;
      setUser(null);
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({
      user,
      loading,
      refreshMe,
      loginWithPassword,
      signup,
      requestMagicLink,
      consumeMagicLink,
      requestPasswordReset,
      confirmPasswordReset,
      verifyEmail,
      logout,
    }),
    [
      user,
      loading,
      refreshMe,
      loginWithPassword,
      signup,
      requestMagicLink,
      consumeMagicLink,
      requestPasswordReset,
      confirmPasswordReset,
      verifyEmail,
      logout,
    ],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
