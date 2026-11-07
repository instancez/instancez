import type { Field, Table } from "./types";

type Tables = Record<string, Partial<Pick<Table, "fields" | "schema">>>;

const AUTH_USERS_ID = "auth.users.id";
const INTEGER_TYPES = ["bigint", "integer", "smallint"];

export function fkOptions(tables: Tables): string[] {
  const out: string[] = [];
  for (const [t, def] of Object.entries(tables)) {
    if (!t) continue;
    const prefix = def.schema && def.schema !== "public" ? `${def.schema}.${t}` : t;
    for (const f of def.fields ?? []) out.push(`${prefix}.${f.name}`);
  }
  out.push(AUTH_USERS_ID);
  return out;
}

function normalize(t: string): string {
  t = t.toLowerCase();
  if (t === "bigserial") return "bigint";
  if (t === "serial" || t === "int") return "integer";
  if (t.startsWith("varchar")) return "varchar";
  if (t === "bool") return "boolean";
  return t;
}

// Like the server's inference, but undefined where the server would default to bigint.
export function fkColumnType(
  ref: string | null | undefined,
  tables: Tables,
  seen: string[] = [],
): string | undefined {
  if (!ref || seen.includes(ref)) return undefined;
  if (ref.toLowerCase() === AUTH_USERS_ID) return "uuid";
  const parts = ref.split(".");
  if (parts.length !== 2 && parts.length !== 3) return undefined;
  const [schema, table, col] = parts.length === 2 ? ["public", ...parts] : parts;
  const t = tables[table as string];
  if (!t || (t.schema || "public") !== schema) return undefined;
  const f: Field | undefined = t.fields?.find((x) => x.name === col);
  if (!f) return undefined;
  const type = f.type || fkColumnType(f.foreign_key?.references, tables, [...seen, ref]);
  if (!type) return undefined;
  const n = normalize(type);
  return INTEGER_TYPES.includes(n) ? "bigint" : n;
}
