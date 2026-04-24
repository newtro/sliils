import { useState } from 'react';
import type { ReactElement } from 'react';
import { Outlet } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthProvider } from '../auth/AuthContext';
import { ThemeProvider } from '../theme/ThemeContext';
import { FirstRunGate } from './FirstRunGate';

export function RootLayout(): ReactElement {
  // One QueryClient per tab. Short staleTime because realtime events
  // already drive cache invalidation; we don't need aggressive refetching.
  const [qc] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { staleTime: 30_000, refetchOnWindowFocus: false, retry: 1 },
          mutations: { retry: 0 },
        },
      }),
  );
  return (
    <ThemeProvider>
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <FirstRunGate>
            <div className="sl-shell">
              <Outlet />
            </div>
          </FirstRunGate>
        </AuthProvider>
      </QueryClientProvider>
    </ThemeProvider>
  );
}
