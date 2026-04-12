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
  on: Record<string, Trigger>;
  functions: Record<string, FunctionDef>;
  seeds: Record<string, Record<string, unknown>[]>;
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

export interface Providers {
  email: { type: string } | null;
  storage: { type: string } | null;
}

export interface Auth {
  jwt_expiry: string;
  refresh_tokens: boolean;
  refresh_token_expiry: string;
  fields: Record<string, Field>;
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
  fields: Record<string, Field>;
  indexes: Index[];
  rls: RLSPolicy[];
  allow_anon: boolean;
  searchable: string[];
  search_config: string;
}

export interface Field {
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

export interface Trigger {
  events: string[];
  schedule: string;
  webhook: WebhookAction | null;
  email: EmailAction | null;
}

export interface WebhookAction {
  url: string;
  headers: Record<string, string>;
  retry: { max: number; backoff: string };
}

export interface EmailAction {
  to: string;
  to_query: string;
  data_query: string;
  subject: string;
  body: string;
  body_file: string;
  condition: string;
}

export interface FunctionDef {
  description: string;
  method: string;
  query: string;
  params: Record<string, FuncParam>;
  returns: FuncReturn;
  auth_required: boolean;
}

export interface FuncParam {
  type: string;
  required: boolean;
  default?: unknown;
  enum?: string[];
  min?: number | null;
  max?: number | null;
}

export interface FuncReturn {
  type: string;
  schema: Record<string, string>;
}

// API response types

export interface StatsResponse {
  tables: Record<string, { row_count: number }>;
  events: {
    last_hour: { delivered: number; failed: number; dead: number };
  };
  storage: Record<string, { object_count: number; total_bytes: number }>;
}

export interface DiffResponse {
  statements: string[];
  is_destructive: boolean;
}

export interface EventRow {
  id: string;
  event: string;
  table: string;
  operation: string;
  status: string;
  attempts: number;
  last_error: string | null;
  created_at: string;
  data: Record<string, unknown>;
  old_data: Record<string, unknown>;
}

export interface ValidationError {
  path: string;
  message: string;
  suggestion?: string;
}
