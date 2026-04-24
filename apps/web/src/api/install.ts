import { apiFetch } from './client';

// Install + integrations API (M12-polish).
//
// Two scopes:
//   - Install-wide policy (signup mode, install status). Read is public
//     so the signup/signin UI can branch; write is admin-only.
//   - Per-workspace email overrides. Fully gated on workspace admin.

export type SignupMode = 'open' | 'invite_only';

export interface InstallStatus {
  setup_completed: boolean;
  signup_mode: SignupMode;
}

export function getInstallStatus(): Promise<InstallStatus> {
  return apiFetch<InstallStatus>('/install/status');
}

export function getSignupMode(): Promise<{ signup_mode: SignupMode }> {
  return apiFetch<{ signup_mode: SignupMode }>('/install/signup-mode');
}

export function setSignupMode(mode: SignupMode): Promise<{ signup_mode: SignupMode }> {
  return apiFetch<{ signup_mode: SignupMode }>('/install/signup-mode', {
    method: 'PATCH',
    body: { signup_mode: mode },
  });
}

// ---- per-workspace email -----------------------------------------------

export interface WorkspaceEmailConfig {
  provider: 'resend';
  from_address?: string;
  from_name?: string;
  api_key_is_set: boolean;
}

export function getWorkspaceEmail(slug: string): Promise<WorkspaceEmailConfig> {
  return apiFetch<WorkspaceEmailConfig>(
    `/workspaces/${encodeURIComponent(slug)}/admin/integrations/email`,
  );
}

export function patchWorkspaceEmail(
  slug: string,
  input: {
    provider?: 'resend';
    resend_api_key?: string; // empty = keep existing
    from_address?: string;
    from_name?: string;
  },
): Promise<WorkspaceEmailConfig> {
  return apiFetch<WorkspaceEmailConfig>(
    `/workspaces/${encodeURIComponent(slug)}/admin/integrations/email`,
    { method: 'PATCH', body: input },
  );
}

export interface EmailTestResult {
  ok: boolean;
  error?: string;
}

export function testWorkspaceEmail(slug: string): Promise<EmailTestResult> {
  return apiFetch<EmailTestResult>(
    `/workspaces/${encodeURIComponent(slug)}/admin/integrations/email/test`,
    { method: 'POST' },
  );
}
