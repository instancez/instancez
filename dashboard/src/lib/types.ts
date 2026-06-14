// TypeScript types matching domain.Config from the Go backend

export interface Config {
  version: number;
  project: Project;
  extensions: string[];
  server: ServerConfig;
  providers: Providers;
  auth: Auth | null;
  tables: Record<string, Table>;
  storage: Record<string, Bucket>;
  rpc: Record<string, RpcFunction>;
  functions: Record<string, CodeFunction>;
  data: Record<string, Record<string, unknown>[] | Record<string, string>>;
  _checksum?: string;
}

export interface Project {
  name: string;
  description: string;
}

export interface ServerConfig {
  port: number;
  cors: CORS;
  timeouts: Timeouts;
  max_body_size: string;
  db: { pool: PoolConfig };
  docs_ui?: boolean;
  max_limit: number;
}

export interface CORS {
  origins: string[];
  methods: string[];
  headers: string[];
  credentials: boolean;
  max_age: number;
}

export interface Timeouts {
  request: string;
  db_query: string;
  upload: string;
  shutdown: string;
}

export interface PoolConfig {
  max: number;
  min: number;
  idle_timeout: string;
}

export interface EmailProviderConfig {
  type: string;
  api_key: string;
  default_from_email: string;
}

export interface StorageProviderConfig {
  type: string;
  bucket: string;
  region: string;
  access_key_id: string;
  secret_access_key: string;
  endpoint: string;
  path: string;
}

export interface Providers {
  email: EmailProviderConfig | null;
  storage: StorageProviderConfig | null;
}

export interface Auth {
  jwt_expiry: string;
  refresh_tokens: boolean;
  refresh_token_expiry: string;
  email: AuthEmail | null;
  google: OAuthProvider | null;
  github: OAuthProvider | null;
}

export interface AuthEmail {
  verify_email: boolean;
  templates: Record<string, EmailTemplate>;
}

export interface EmailTemplate {
  subject: string;
  body: string;
  body_file: string;
}

export interface OAuthProvider {
  client_id: string;
  client_secret: string;
  redirect_url: string;
}

export interface Table {
  fields: Field[];
  indexes: Index[];
  rls: RLSPolicy[];
}

export interface Field {
  name: string;
  type: string;
  primary_key?: boolean;
  required?: boolean;
  unique?: boolean;
  default?: unknown;
  enum?: string[];
  pattern?: string;
  min?: number | null;
  max?: number | null;
  check?: string;
  foreign_key?: ForeignKey | null;
  ref?: string;
  on_delete?: string;
}

export interface ForeignKey {
  references: string;
  on_delete: string;
}

export interface Index {
  columns: string[];
  unique: boolean;
  where: string;
}

export interface RLSPolicy {
  operations: string[];
  check: string;
  type?: string; // "permissive" (default) | "restrictive"
}

export interface Bucket {
  max_size: string;
  types: string[];
  public: boolean;
  rls: RLSPolicy[];
}

export interface RpcFunction {
  description: string;
  auth_required: boolean;
  language: string;
  volatility: string;
  security: string;
  args: FuncArg[];
  body: string;
  returns: FuncReturn;
}

export interface CodeFunction {
  runtime: string;
  file: string;
  auth_required: boolean;
  timeout?: string;
  env?: Record<string, string>;
}

export interface FuncArg {
  name: string;
  type: string;
  required: boolean;
  default?: unknown;
}

export interface FuncReturn {
  type: string;
}

// API response types

export interface StatsResponse {
  tables: Record<string, { row_count: number }>;
  storage: Record<string, { object_count: number; total_bytes: number }>;
}

export interface DiffResponse {
  statements: string[];
  is_destructive: boolean;
}

export interface ValidationError {
  path: string;
  message: string;
  suggestion?: string;
}

export type ConfigStatus = {
  status: "ok" | "drift" | "unknown";
  config_source: string;
  running: { checksum: string; applied_at: string };
  source: { checksum: string; last_seen_at: string };
  last_error: string | null;
  dashboard_mode: "disabled" | "readonly" | "readwrite";
  dotenv_writable?: boolean;
};

export interface AdminUser {
  id: string;
  email: string;
  email_confirmed_at: string;
  banned_until: string;
  last_sign_in_at: string;
  app_metadata: Record<string, unknown>;
  user_metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}
