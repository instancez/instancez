import type {
  Config,
  ConfigStatus,
  StatsResponse,
  DiffResponse,
  ValidationError,
} from "../lib/types";

const BASE = "/api/_admin";

function getAdminKey(): string {
  return sessionStorage.getItem("instancez_admin_key") || "";
}

async function request<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const key = getAdminKey();
  if (!key) throw new Error("No admin key configured");

  const res = await fetch(`${BASE}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${key}`,
      ...options.headers,
    },
  });

  if (res.status === 401) {
    sessionStorage.removeItem("instancez_admin_key");
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

export async function getConfigDiff(): Promise<DiffResponse> {
  return request<DiffResponse>("/config/diff");
}

export async function getConfigStatus(): Promise<ConfigStatus> {
  return request<ConfigStatus>("/config/status");
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

// Validate admin key by calling status
export async function validateAdminKey(key: string): Promise<boolean> {
  try {
    const res = await fetch(`${BASE}/status`, {
      headers: { Authorization: `Bearer ${key}` },
    });
    return res.ok;
  } catch {
    return false;
  }
}
