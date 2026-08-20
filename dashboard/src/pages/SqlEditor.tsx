import { useCallback, useState } from "react";
import { Box, HStack, VStack, Text } from "@chakra-ui/react";
import { Play, Download } from "lucide-react";
import { CodeEditor } from "../components/CodeEditor";
import { Button } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import type { SqlResult } from "../lib/types";

function csvCell(v: unknown): string {
  if (v == null) return "";
  const s = typeof v === "object" ? JSON.stringify(v) : String(v);
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}
function exportCsv(r: SqlResult) {
  const lines = [r.columns.map(csvCell).join(","), ...r.rows.map((row) => row.map(csvCell).join(","))];
  const url = URL.createObjectURL(new Blob([lines.join("\n")], { type: "text/csv" }));
  const a = document.createElement("a"); a.href = url; a.download = "query-results.csv"; a.click();
  URL.revokeObjectURL(url);
}

export function SqlEditor() {
  const backend = useBackend();
  const [sql, setSql] = useState("select * from ");
  const [result, setResult] = useState<SqlResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [running, setRunning] = useState(false);

  const run = useCallback(async () => {
    if (!sql.trim() || running) return;
    setRunning(true); setError(null);
    try { setResult(await backend.runQuery(sql)); }
    catch (e) { setError((e as Error)?.message || "Query failed."); setResult(null); }
    finally { setRunning(false); }
  }, [backend, sql, running]);

  const truncated = (result?.row_count ?? 0) >= 1000;
  return (
    <VStack align="stretch" gap="0" height="100%">
      <HStack px="3" py="2" borderBottomWidth="1px" borderColor="border" gap="3">
        <Button size="sm" onClick={run} disabled={running}><Play size={13} /> Run</Button>
        <Text fontSize="xs" color="fg.subtle" fontFamily="mono">⌘↵</Text>
      </HStack>
      <Box borderBottomWidth="1px" borderColor="border"><CodeEditor language="sql" value={sql} onChange={setSql} minHeight="220px" /></Box>
      {error && <Box px="3" py="3"><Text fontSize="sm" color="fg.error" fontFamily="mono" whiteSpace="pre-wrap">{error}</Text></Box>}
      {result && (
        <>
          <HStack justify="space-between" px="3" py="2" borderBottomWidth="1px" borderColor="border">
            <Text fontSize="xs" color="fg.muted" fontFamily="mono">
              {result.row_count} row{result.row_count === 1 ? "" : "s"}{truncated ? " · first 1000 shown" : ""}
            </Text>
            <Button size="xs" variant="outline" onClick={() => exportCsv(result)}><Download size={12} /> Export CSV</Button>
          </HStack>
          <Box overflow="auto">
            <Box as="table" width="100%" fontFamily="mono" fontSize="xs" css={{ borderCollapse: "collapse" }}>
              <Box as="thead"><Box as="tr" bg="bg.subtle" color="fg.muted">
                {result.columns.map((c) => <Box as="th" key={c} textAlign="left" px="3" py="2" borderBottomWidth="1px" borderColor="border">{c}</Box>)}
              </Box></Box>
              <Box as="tbody">
                {result.rows.map((row, ri) => <Box as="tr" key={ri}>
                  {row.map((cell, ci) => <Box as="td" key={ci} px="3" py="2" borderBottomWidth="1px" borderColor="border">
                    {cell == null ? "" : typeof cell === "object" ? JSON.stringify(cell) : String(cell)}
                  </Box>)}
                </Box>)}
              </Box>
            </Box>
          </Box>
        </>
      )}
    </VStack>
  );
}
