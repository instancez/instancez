import type {
  Config,
  ConfigStatus,
  StatsResponse,
  DiffResponse,
  ValidationError,
  AdminUser,
} from "../lib/types";

const BASE = "/api/_admin";

function getSecretKey(): string {
  return sessionStorage.getItem("instancez_secret_key") || "";
}

async function request<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const key = getSecretKey();
  if (!key) throw new Error("No secret key configured");

  const res = await fetch(`${BASE}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${key}`,
      apikey: key,
      ...options.headers,
    },
  });

  if (res.status === 401) {
    sessionStorage.removeItem("instancez_secret_key");
    window.location.reload();
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const err = new Error(body?.message || body?.error || `HTTP ${res.status}`);
    (err as any).status = res.status;
    (err as any).body = body;
    throw err;
  }

  return res.json();
}

// Config
export async function getConfig(): Promise<Config> {
  return request<Config>("/config");
}

export async function putConfig(
  config: Omit<Config, "_checksum">,
  checksum: string
): Promise<{ message: string; config_source?: string }> {
  return request("/config", {
    method: "PUT",
    headers: { "If-Match": checksum },
    body: JSON.stringify(config),
  });
}

export interface ConfigPreview {
  current: string;
  proposed: string;
}

/** Dry-run of a save: returns current and would-be instancez.yaml contents. */
export async function previewConfig(config: Omit<Config, "_checksum">): Promise<ConfigPreview> {
  return request<ConfigPreview>("/config/preview", {
    method: "POST",
    body: JSON.stringify(config),
  });
}

export async function getConfigDiff(): Promise<DiffResponse> {
  return request<DiffResponse>("/config/diff");
}

export async function getConfigStatus(): Promise<ConfigStatus> {
  return request<ConfigStatus>("/config/status");
}

/** Existence probe for a function file path (relative to the config root). */
export async function checkFunctionFile(file: string): Promise<{ exists: boolean }> {
  return request<{ exists: boolean }>(
    `/functions/file-exists?file=${encodeURIComponent(file)}`
  );
}

export interface EnvVarsResponse {
  // tail is the last-4 chars of the value, present only when set: a masked
  // confirmation of which value landed. The plaintext is never returned.
  vars: Record<string, { set: boolean; tail?: string }>;
}

export async function getEnvVars(names?: string[]): Promise<EnvVarsResponse> {
  const query = names?.length ? `?names=${encodeURIComponent(names.join(","))}` : "";
  return request<EnvVarsResponse>(`/config/env-vars${query}`);
}

export async function putDotenv(
  vars: Record<string, string>
): Promise<{ message: string }> {
  return request("/config/dotenv", {
    method: "PUT",
    body: JSON.stringify(vars),
  });
}

// Stats
export async function getStats(): Promise<StatsResponse> {
  return request<StatsResponse>("/stats");
}

// Status
export async function getStatus(): Promise<Record<string, unknown>> {
  return request("/status");
}

// Migrations
export async function getMigrations(): Promise<
  { id: number; checksum: string; applied_at: string }[]
> {
  return request("/migrations");
}

// Users
export async function getUsers(): Promise<
  { id: number; email: string; email_verified: boolean; created_at: string }[]
> {
  return request("/users");
}

// ── Auth admin user management (/auth/v1/admin/users) ────────────────────
// Uses the Supabase-compatible endpoint surface, not /_admin/users.

const AUTH_ADMIN_BASE = "/auth/v1/admin";

async function rawAuthAdminFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const key = getSecretKey();
  if (!key) throw new Error("No secret key configured");
  return fetch(`${AUTH_ADMIN_BASE}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${key}`,
      apikey: key,
      ...options.headers,
    },
  });
}

async function authAdminRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const res = await rawAuthAdminFetch(path, options);
  if (res.status === 401) {
    sessionStorage.removeItem("instancez_secret_key");
    window.location.reload();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const err = new Error(body?.message || body?.error || `HTTP ${res.status}`);
    (err as any).status = res.status;
    (err as any).body = body;
    throw err;
  }
  return res.json();
}

export async function adminListUsers(
  page = 1,
  perPage = 50
): Promise<{ users: AdminUser[]; total: number }> {
  const res = await rawAuthAdminFetch(`/users?page=${page}&per_page=${perPage}`);
  if (res.status === 401) {
    sessionStorage.removeItem("instancez_secret_key");
    window.location.reload();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const err = new Error(body?.message || body?.error || `HTTP ${res.status}`);
    (err as any).status = res.status;
    (err as any).body = body;
    throw err;
  }
  const raw = res.headers.get("x-total-count");
  const total = raw !== null ? (parseInt(raw, 10) || 0) : 0;
  const data = await res.json();
  return { users: data.users ?? [], total };
}

export async function adminCreateUser(
  email: string,
  password: string,
  emailConfirm: boolean
): Promise<AdminUser> {
  return authAdminRequest<AdminUser>("/users", {
    method: "POST",
    body: JSON.stringify({ email, password, email_confirm: emailConfirm }),
  });
}

export async function adminUpdateUser(
  id: string,
  patch: { email?: string; password?: string; ban_duration?: string; email_confirm?: boolean }
): Promise<AdminUser> {
  return authAdminRequest<AdminUser>(`/users/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(patch),
  });
}

export async function adminDeleteUser(id: string): Promise<void> {
  await authAdminRequest<unknown>(`/users/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

// API keys: the browser-safe publishable key. The backend also returns the
// secret key, but the console reads that from sessionStorage (what the user
// logged in with), so only the publishable key is declared here.
export async function getKeys(): Promise<{ publishable_key: string }> {
  return request("/keys");
}

// Function code (dev / readwrite mode only)
export async function getFunctionCode(name: string): Promise<{ content: string; file: string }> {
  return request(`/functions/${encodeURIComponent(name)}/code`);
}

export async function putFunctionCode(name: string, content: string): Promise<{ message: string }> {
  return request(`/functions/${encodeURIComponent(name)}/code`, {
    method: "PUT",
    body: JSON.stringify({ content }),
  });
}

// Function npm dependencies
export async function getFunctionDeps(): Promise<{
  dependencies: Record<string, string>;
  has_lock: boolean;
  readonly: boolean;
}> {
  return request("/functions/deps");
}

export async function postFunctionDeps(
  add: string[],
  remove: string[]
): Promise<{ dependencies: Record<string, string>; has_lock: boolean; readonly: boolean }> {
  return request("/functions/deps", {
    method: "POST",
    body: JSON.stringify({ add, remove }),
  });
}

// Validate the secret key by calling status
export async function validateSecretKey(key: string): Promise<boolean> {
  try {
    const res = await fetch(`${BASE}/status`, {
      headers: { Authorization: `Bearer ${key}`, apikey: key },
    });
    return res.ok;
  } catch {
    return false;
  }
}
