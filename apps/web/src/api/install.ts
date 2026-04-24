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

// ---- install-level email (super-admin only) ----------------------------

export interface InstallEmailConfig {
  provider: 'resend';
  from_address?: string;
  from_name?: string;
  api_key_is_set: boolean;
}

export function getInstallEmail(): Promise<InstallEmailConfig> {
  return apiFetch<InstallEmailConfig>('/install/email');
}

export function patchInstallEmail(input: {
  provider?: 'resend';
  resend_api_key?: string; // empty = keep existing
  from_address?: string;
  from_name?: string;
}): Promise<InstallEmailConfig> {
  return apiFetch<InstallEmailConfig>('/install/email', {
    method: 'PATCH',
    body: input,
  });
}

// ---- install-level infrastructure (super-admin only) -------------------

export interface InstallInfrastructure {
  vapid_public_key?: string;
  vapid_private_key_set: boolean;
  vapid_subject?: string;
  collabora_url?: string;
  ysweet_url?: string;
  ysweet_server_token_set: boolean;
  livekit_url?: string;
  livekit_ws_url?: string;
  livekit_api_key?: string;
  livekit_api_secret_set: boolean;
}

export interface PatchInstallInfrastructure {
  vapid_public_key?: string;
  vapid_private_key?: string; // empty string = keep existing
  vapid_subject?: string;
  collabora_url?: string;
  ysweet_url?: string;
  ysweet_server_token?: string; // empty = keep
  livekit_url?: string;
  livekit_ws_url?: string;
  livekit_api_key?: string;
  livekit_api_secret?: string; // empty = keep
}

export function getInstallInfrastructure(): Promise<InstallInfrastructure> {
  return apiFetch<InstallInfrastructure>('/install/infrastructure');
}

export function patchInstallInfrastructure(
  input: PatchInstallInfrastructure,
): Promise<InstallInfrastructure> {
  return apiFetch<InstallInfrastructure>('/install/infrastructure', {
    method: 'PATCH',
    body: input,
  });
}

export interface VAPIDKeypair {
  public_key: string;
  private_key: string;
}

export function generateVAPIDKeys(): Promise<VAPIDKeypair> {
  return apiFetch<VAPIDKeypair>('/install/vapid/generate', { method: 'POST' });
}
