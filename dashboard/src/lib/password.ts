const ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";

/** A random password from an unambiguous alphabet, using the browser CSPRNG. */
export function generatePassword(length = 16): string {
  const buf = new Uint32Array(length);
  crypto.getRandomValues(buf);
  return Array.from(buf, (n) => ALPHABET[n % ALPHABET.length]).join("");
}

export type PasswordStrength = { level: "empty" | "short" | "weak" | "good" | "strong"; label: string };

/** Rough hint only: length first, then character-class variety. The server enforces the real minimum (6). */
export function passwordStrength(pw: string): PasswordStrength {
  if (!pw) return { level: "empty", label: "" };
  if (pw.length < 6) return { level: "short", label: "Too short, use at least 6 characters" };
  const classes = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length;
  if (pw.length >= 14 && classes >= 3) return { level: "strong", label: "Strong" };
  if (pw.length >= 10 && classes >= 2) return { level: "good", label: "Good" };
  return { level: "weak", label: "Weak, try a longer mix of letters and numbers" };
}
