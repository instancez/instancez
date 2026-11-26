import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Box, HStack, VStack, Text } from "@chakra-ui/react";
import type { SQLNamespace } from "@codemirror/lang-sql";
import { Play, Download, AlignLeft, ArrowUp, ArrowDown } from "lucide-react";
import { CodeEditor, type CodeEditorHandle } from "../components/CodeEditor";
import { CopyButton } from "../components/CopyButton";
import { identifiersFromConfig, richSchema, type SqlIdentifiers } from "../components/sqlIdentifiers";
import { formatSql } from "../components/sqlTools";
import { Button, Panel } from "../components/ui";
import { useApiBaseUrl, useBackend } from "../console/BackendContext";
import type { SqlResult } from "../lib/types";

const DEFAULT_SQL = "select * from ";

export function cellText(v: unknown): string {
  if (v == null) return "";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

function csvCell(v: unknown): string {
  const s = cellText(v);
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}
function exportCsv(r: SqlResult) {
  const lines = [(r.columns ?? []).map(csvCell).join(","), ...(r.rows ?? []).map((row) => row.map(csvCell).join(","))];
  const url = URL.createObjectURL(new Blob([lines.join("\n")], { type: "text/csv" }));
  const a = document.createElement("a"); a.href = url; a.download = "query-results.csv"; a.click();
  URL.revokeObjectURL(url);
}

// Cells arrive as strings in readwrite mode, so numbers are detected from text.
const INT = /^-?\d+$/;
const asNumber = (v: unknown) => (typeof v === "number" ? v : typeof v === "string" && v.trim() !== "" ? Number(v) : NaN);

/** Nulls last, numbers numerically, everything else as natural-order text. */
export function sortRows(rows: unknown[][], col: number, dir: "asc" | "desc"): unknown[][] {
  const sign = dir === "asc" ? 1 : -1;
  return [...rows].sort((a, b) => {
    const x = a[col], y = b[col];
    if (x == null || y == null) return x == null ? (y == null ? 0 : 1) : -1;
    // Integer text compares exactly, so bigints past 2^53 keep their order.
    if (typeof x === "string" && typeof y === "string" && INT.test(x) && INT.test(y)) {
      const d = BigInt(x) - BigInt(y);
      return (d === 0n ? 0 : d > 0n ? 1 : -1) * sign;
    }
    const nx = asNumber(x), ny = asNumber(y);
    if (Number.isFinite(nx) && Number.isFinite(ny)) return (nx - ny) * sign;
    return cellText(x).localeCompare(cellText(y), undefined, { numeric: true }) * sign;
  });
}

/** One row as an object; repeated column names (`select 1, 2`) get a `_2`, `_3` suffix so none are lost. */
export function rowObject(columns: string[], row: unknown[]): Record<string, unknown> {
  const out: Record<string, unknown> = Object.create(null); // so "constructor" or "__proto__" are plain keys
  columns.forEach((c, i) => {
    let key = c;
    for (let n = 2; Object.prototype.hasOwnProperty.call(out, key); n++) key = `${c}_${n}`;
    out[key] = row[i];
  });
  return out;
}

function loadDraft(key: string): string {
  try {
    return localStorage.getItem(key) ?? DEFAULT_SQL;
  } catch {
    return DEFAULT_SQL;
  }
}

export function SqlEditor() {
  const draftKey = `inz:sql-draft:${useApiBaseUrl()}`;
  // Remount per project so one project's draft is never saved under another's key.
  return <SqlEditorPane key={draftKey} draftKey={draftKey} />;
}

function SqlEditorPane({ draftKey }: { draftKey: string }) {
  const backend = useBackend();
  const editor = useRef<CodeEditorHandle>(null);
  const [sql, setSql] = useState(() => loadDraft(draftKey));
  const [result, setResult] = useState<SqlResult | null>(null);
  const [elapsed, setElapsed] = useState<number | null>(null);
  const [sort, setSort] = useState<{ col: number; dir: "asc" | "desc" } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [schema, setSchema] = useState<SQLNamespace>();
  const [identifiers, setIdentifiers] = useState<SqlIdentifiers>();

  useEffect(() => {
    backend
      .getConfig()
      .then((cfg) => {
        setIdentifiers(identifiersFromConfig(cfg));
        setSchema(richSchema(cfg));
      })
      .catch(() => {}); // autocomplete is a nicety; a failed load just means keywords only
  }, [backend]);

  useEffect(() => {
    try {
      localStorage.setItem(draftKey, sql);
    } catch {
      /* storage unavailable (private mode, quota): the draft just won't persist */
    }
  }, [draftKey, sql]);

  const run = useCallback(async () => {
    const query = editor.current?.runnable() ?? sql;
    if (!query.trim() || running) return;
    setRunning(true); setError(null);
    const started = performance.now();
    try {
      // Postgres/Go returns null (not []) for a zero-row or non-SELECT result;
      // normalize so the grid never maps over null.
      const r = await backend.runQuery(query);
      setResult({ columns: r.columns ?? [], rows: r.rows ?? [], row_count: r.row_count ?? 0 });
      setElapsed(Math.round(performance.now() - started));
      setSort(null);
    }
    catch (e) { setError((e as Error).message || "Query failed."); setResult(null); }
    finally { setRunning(false); }
  }, [backend, sql, running]);

  const rows = useMemo(
    () => (result && sort ? sortRows(result.rows ?? [], sort.col, sort.dir) : (result?.rows ?? [])),
    [result, sort]
  );
  const toggleSort = (col: number) =>
    setSort((s) => (s?.col !== col ? { col, dir: "asc" } : s.dir === "asc" ? { col, dir: "desc" } : null));

  const truncated = (result?.row_count ?? 0) >= 1000;
  const columns = result?.columns ?? [];
  return (
    <VStack align="stretch" gap="4">
      <Panel overflow="hidden">
        <Box css={{ "& .cm-editor": { border: "none", borderRadius: 0, boxShadow: "none" }, "& .cm-scroller": { borderRadius: 0 } }}>
          <CodeEditor
            ref={editor}
            language="sql"
            value={sql}
            onChange={setSql}
            onSubmit={() => void run()}
            sqlSchema={schema}
            sqlIdentifiers={identifiers}
            sqlLint
            format={formatSql}
            minHeight="220px"
          />
        </Box>
        <HStack justify="space-between" px="4" py="3" borderTopWidth="1px" borderColor="border">
          <Button size="sm" variant="outline" onClick={() => void editor.current?.format()}><AlignLeft size={13} /> Format</Button>
          <Button size="sm" onClick={() => void run()} disabled={running}><Play size={13} /> Run</Button>
        </HStack>
      </Panel>
      {error && <Panel px="3" py="3" borderColor="border.error"><Text fontSize="sm" color="fg.error" fontFamily="mono" whiteSpace="pre-wrap">{error}</Text></Panel>}
      {result && (
        <Panel overflow="hidden">
          <HStack justify="space-between" px="3" py="2" borderBottomWidth="1px" borderColor="border">
            <Text fontSize="xs" color="fg.muted" fontFamily="mono">
              {result.row_count} row{result.row_count === 1 ? "" : "s"}{truncated ? " · first 1000 shown" : ""}{elapsed !== null ? ` · ${elapsed} ms` : ""}
            </Text>
            {columns.length > 0 && <Button size="xs" variant="outline" onClick={() => { exportCsv(result); }}><Download size={12} /> Export CSV</Button>}
          </HStack>
          {columns.length === 0 ? (
            <Text px="3" py="6" fontSize="sm" color="fg.muted" textAlign="center">Query ran successfully. No rows returned.</Text>
          ) : (
            <Box overflow="auto">
              <Box as="table" width="100%" fontFamily="mono" fontSize="xs" css={{ borderCollapse: "collapse" }}>
                <Box as="thead"><Box as="tr" bg="bg.subtle" color="fg.muted">
                  {columns.map((c, ci) => (
                    <Box as="th" key={ci} textAlign="left" px="3" py="2" borderBottomWidth="1px" borderColor="border" aria-sort={sort?.col === ci ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}>
                      <Box as="button" display="inline-flex" alignItems="center" gap="1" cursor="pointer" fontWeight="inherit" onClick={() => toggleSort(ci)} aria-label={`Sort by ${c}`}>
                        {c}
                        {sort?.col === ci && (sort.dir === "asc" ? <ArrowUp size={11} /> : <ArrowDown size={11} />)}
                      </Box>
                    </Box>
                  ))}
                  <Box as="th" borderBottomWidth="1px" borderColor="border" width="1px" />
                </Box></Box>
                <Box as="tbody">
                  {rows.length === 0 && (
                    <Box as="tr"><Box as="td" {...{ colSpan: columns.length + 1 }} px="3" py="6" textAlign="center" color="fg.muted">No rows returned.</Box></Box>
                  )}
                  {rows.map((row, ri) => <Box as="tr" key={ri} _hover={{ bg: "bg.subtle" }}>
                    {row.map((cell, ci) => <Box as="td" key={ci} px="3" py="2" borderBottomWidth="1px" borderColor="border" cursor="copy" title="Click to copy" onClick={() => void navigator.clipboard?.writeText(cellText(cell)).catch(() => {})}>
                      {cellText(cell)}
                    </Box>)}
                    <Box as="td" px="1" borderBottomWidth="1px" borderColor="border">
                      <CopyButton label="row" value={JSON.stringify(rowObject(columns, row))} />
                    </Box>
                  </Box>)}
                </Box>
              </Box>
            </Box>
          )}
        </Panel>
      )}
    </VStack>
  );
}
