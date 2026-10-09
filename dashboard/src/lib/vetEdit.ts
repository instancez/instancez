import type { Config, VetEdit } from "./types";

type Node = Record<string | number, unknown>;

const PROTOTYPE_KEYS = ["__proto__", "constructor", "prototype"];

function apply(node: Node, path: (string | number)[], edit: VetEdit): boolean {
  const [key, ...rest] = path;
  if (key === undefined) return true;
  if (rest.length > 0) {
    node[key] ??= typeof rest[0] === "number" ? [] : {};
    return apply(node[key] as Node, rest, edit);
  }
  if (!edit.remove) node[key] = edit.value;
  else if (edit.value != null && node[key] !== edit.value) return false;
  else if (Array.isArray(node)) node.splice(Number(key), 1);
  else delete node[key];
  return true;
}

/**
 * Returns a copy of config with the edit applied, creating missing objects along the path.
 * Returns null when a remove edit no longer finds its expected value, or when the path holds a prototype key.
 */
export function applyVetEdit(config: Config, edit: VetEdit): Config | null {
  if (edit.path.some((k) => PROTOTYPE_KEYS.includes(String(k)))) return null;
  const next = structuredClone(config);
  return apply(next as unknown as Node, edit.path, edit) ? next : null;
}
