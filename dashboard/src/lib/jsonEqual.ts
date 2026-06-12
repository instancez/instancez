/**
 * Structural equality for config objects (plain JSON data). Used to derive
 * "dirty" by comparing edited local state against the saved config, so the
 * save bar disappears again when an edit is undone.
 */
export function jsonEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(a ?? null) === JSON.stringify(b ?? null);
}
