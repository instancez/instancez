import type { Config, VetEdit } from "./types";

type Node = Record<string | number, unknown>;

/**
 * Returns a copy of config with the edit applied, creating missing objects along the path.
 * Returns null when a remove edit no longer finds its expected value, so a stale scan cannot delete the wrong entry.
 */
export function applyVetEdit(config: Config, edit: VetEdit): Config | null {
  const next = structuredClone(config);
  const last = edit.path.at(-1);
  if (last === undefined) return next;
  let node = next as unknown as Node;
  edit.path.slice(0, -1).forEach((key, i) => {
    node[key] ??= typeof edit.path[i + 1] === "number" ? [] : {};
    node = node[key] as Node;
  });
  if (!edit.remove) node[last] = edit.value;
  else if (edit.value != null && node[last] !== edit.value) return null;
  else if (Array.isArray(node)) node.splice(Number(last), 1);
  else delete node[last];
  return next;
}
