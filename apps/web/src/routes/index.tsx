import type { RouteObject } from 'react-router';
import { RootLayout } from '../components/RootLayout';
import { HomePage } from '../pages/HomePage';
import { LoginPage } from '../pages/LoginPage';
import { MarketingPage } from '../pages/MarketingPage';
import { SignupPage } from '../pages/SignupPage';
import { MagicLinkPage } from '../pages/MagicLinkPage';
import { ForgotPasswordPage, ResetPasswordPage } from '../pages/ForgotPasswordPage';
import { VerifyEmailPage } from '../pages/VerifyEmailPage';
import { ProfilePage } from '../pages/ProfilePage';
import { SetupPage } from '../pages/SetupPage';
import { WorkspacePage } from '../pages/WorkspacePage';
import { InvitePage } from '../pages/InvitePage';
import { CalendarPage } from '../pages/CalendarPage';
import { PagesPage } from '../pages/PagesPage';
import { AdminPage } from '../pages/AdminPage';
import { FirstRunPage } from '../pages/FirstRunPage';

export const routes: RouteObject[] = [
  {
    path: '/',
    Component: RootLayout,
    children: [
      { index: true, Component: HomePage },
      { path: 'first-run', Component: FirstRunPage },
      { path: 'marketing', Component: MarketingPage },
      { path: 'login', Component: LoginPage },
      { path: 'signup', Component: SignupPage },
      { path: 'magic-link', Component: MagicLinkPage },
      { path: 'auth/magic-link', Component: MagicLinkPage },
      { path: 'forgot-password', Component: ForgotPasswordPage },
      { path: 'reset-password', Component: ResetPasswordPage },
      { path: 'auth/reset-password', Component: ResetPasswordPage },
      { path: 'auth/verify-email', Component: VerifyEmailPage },
      { path: 'me', Component: ProfilePage },
      { path: 'setup', Component: SetupPage },
      { path: 'w/:slug', Component: WorkspacePage },
      { path: 'w/:slug/calendar', Component: CalendarPage },
      { path: 'w/:slug/pages', Component: PagesPage },
      { path: 'w/:slug/pages/:pageId', Component: PagesPage },
      { path: 'w/:slug/admin', Component: AdminPage },
      { path: 'invite/:token', Component: InvitePage },
    ],
  },
];
