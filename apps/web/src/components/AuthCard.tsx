import type { ReactElement, ReactNode } from 'react';
import { ThemeToggle } from '../theme/ThemeToggle';

interface AuthCardProps {
  heading: string;
  subtext?: string;
  children?: ReactNode;
  footer?: ReactNode;
}

export function AuthCard({ heading, subtext, children, footer }: AuthCardProps): ReactElement {
  return (
    <main className="sl-placeholder" aria-labelledby="auth-heading">
      <ThemeToggle className="sl-theme-toggle-float" />
      <div className="sl-card sl-auth-card">
        <h1 id="auth-heading" className="sl-auth-heading" aria-label="SliilS">
          {'Sl'}
          <span className="sl-i-green" aria-hidden="true">i</span>
          <span className="sl-i-blue" aria-hidden="true">i</span>
          {'lS'}
        </h1>
        <h2 className="sl-auth-subheading">{heading}</h2>
        {subtext && <p className="sl-auth-subtext">{subtext}</p>}
        {children}
        {footer && <div className="sl-auth-footer">{footer}</div>}
      </div>
    </main>
  );
}
