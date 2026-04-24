import { apiFetch } from './client';

// First-run wizard API (M12-polish).
//
// Flow:
//   GET /first-run/state → { completed, users_count }
//   if !completed, the web client routes to /first-run, collects admin +
//   email + signup mode + first workspace, and POSTs everything in one
//   shot. The response carries an access_token so the wizard can log
//   the new admin in immediately.

export interface FirstRunState {
  completed: boolean;
  users_count: number;
  signup_mode: 'open' | 'invite_only';
}

export interface BootstrapInput {
  admin: {
    email: string;
    password: string;
    display_name: string;
  };
  email: {
    provider?: 'resend';
    resend_api_key?: string;
    from_address?: string;
    from_name?: string;
  };
  signup_mode: 'open' | 'invite_only';
  workspace: {
    name: string;
    slug: string;
    description?: string;
  };
}

export interface BootstrapResult {
  access_token: string;
  token_type: 'Bearer';
  expires_at: string;
  workspace_slug: string;
  user_id: number;
}

export function getFirstRunState(): Promise<FirstRunState> {
  return apiFetch<FirstRunState>('/first-run/state');
}

export function bootstrapInstall(input: BootstrapInput): Promise<BootstrapResult> {
  return apiFetch<BootstrapResult>('/first-run/bootstrap', {
    method: 'POST',
    body: input,
    skipRefresh: true,
  });
}
