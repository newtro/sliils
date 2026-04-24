import type { ReactElement } from 'react';
import { useTheme } from './ThemeContext';

interface Props {
  className?: string;
}

/**
 * Single-click theme toggle. One of the two SVGs is hidden via CSS depending
 * on the current theme; both are rendered so the icon swap animates via
 * opacity/rotation rather than a jarring element swap.
 */
export function ThemeToggle({ className }: Props): ReactElement {
  const { resolved, toggle } = useTheme();
  const label = resolved === 'dark' ? 'Switch to light mode' : 'Switch to dark mode';

  return (
    <button
      type="button"
      className={`sl-theme-toggle ${className ?? ''}`}
      onClick={toggle}
      aria-label={label}
      title={label}
      data-resolved={resolved}
    >
      <svg
        className="sl-theme-icon sl-theme-icon-sun"
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
      </svg>
      <svg
        className="sl-theme-icon sl-theme-icon-moon"
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
      </svg>
    </button>
  );
}
