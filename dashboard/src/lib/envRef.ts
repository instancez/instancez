const ENV_REF_RE = /^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$/;

/**
 * Returns the env var name when a config value is exactly a ${VAR} reference
 * (the YAML form for env-supplied configs), null for literal values.
 */
export function envRefName(value: unknown): string | null {
  if (typeof value !== "string") return null;
  return ENV_REF_RE.exec(value)?.[1] ?? null;
}
