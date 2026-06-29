/**
 * Reports whether value is usable as an `auth.redirect_urls` allowlist entry.
 *
 * Entries are external origins the backend may redirect to after OAuth and
 * email flows. This mirrors the absolute-URL arm of the server's
 * `Auth.IsRedirectAllowed`: only an `http(s)://host` origin makes sense to
 * list. The server already allows its own origin and relative paths, so those
 * are rejected here as pointless (and protocol-relative / backslash forms are
 * rejected as parser-differential footguns). The path component is accepted
 * but irrelevant; the server compares origins only.
 *
 * This is the only validation of the list; the backend does not check it.
 */
export function isValidRedirectOrigin(value: string): boolean {
  if (!value) return false;
  // Browsers fold backslashes into slashes; reject them (and NULs) outright so
  // a listed entry can't differ from what the server's url parser sees.
  if (/[\\\x00]/.test(value)) return false;
  let u: URL;
  try {
    u = new URL(value);
  } catch {
    return false;
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return false;
  return u.host !== "";
}
