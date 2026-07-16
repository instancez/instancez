/**
 * Reports whether value is usable as a `server.cors.origins` allowlist entry:
 * the wildcard, or an absolute http(s) origin. Mirrors the engine's own match
 * in corsMiddleware (exact string or "*"); this is browser-side hinting only,
 * the backend does not check it.
 */
export function isValidCorsOrigin(value: string): boolean {
  if (value === "*") return true;
  if (!value) return false;
  if (/[\\\x00]/.test(value)) return false;
  let u: URL;
  try {
    u = new URL(value);
  } catch {
    return false;
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return false;
  return u.host !== "" && u.pathname === "/" && !u.search && !u.hash;
}
