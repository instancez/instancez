import type { Field } from "./types";

type Tables = Record<string, { fields?: Field[] }>;

const AUTH_USERS_ID = "auth.users.id";

function parse(ref: string): { schema: string; table: string; col: string } | null {
  const [a, b, c, ...rest] = ref.split(".");
  if (!a || !b || rest.length) return null;
  return c === undefined ? { schema: "public", table: a, col: b } : c ? { schema: a, table: b, col: c } : null;
}

export function fkOptions(tables: Tables): string[] {
  const out: string[] = [];
  for (const [t, def] of Object.entries(tables)) {
    for (const f of def.fields ?? []) out.push(`${t}.${f.name}`);
  }
  out.push(AUTH_USERS_ID);
  return out;
}

// Returns undefined when the referenced column's type is not known.
export function fkColumnType(ref: string | null | undefined, tables: Tables): string | undefined {
  if (!ref) return undefined;
  const r = parse(ref);
  if (!r) return undefined;
  if (`${r.schema}.${r.table}.${r.col}` === AUTH_USERS_ID) return "uuid";
  if (r.schema !== "public") return undefined;
  return tables[r.table]?.fields?.find((f) => f.name === r.col)?.type || undefined;
}
