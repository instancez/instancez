import { useEffect, useState } from "react";
import { Box, HStack, Text } from "@chakra-ui/react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "./ui";
import { useBackend } from "../console/BackendContext";
import { DATA_PAGE_SIZE, pageQuery } from "../lib/tableQuery";

interface Page { columns: string[]; rows: unknown[][]; hasNext: boolean }

const show = (v: unknown) => (v == null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v));

export function TableData({ name, primaryKey }: { name: string; primaryKey?: string }) {
  const backend = useBackend();
  const [page, setPage] = useState(0);
  const [data, setData] = useState<Page | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    setLoading(true);
    backend.runQuery(pageQuery(name, primaryKey, page))
      .then((r) => {
        if (!active) return;
        const rows = r.rows ?? [];
        setData({ columns: r.columns ?? [], rows: rows.slice(0, DATA_PAGE_SIZE), hasNext: rows.length > DATA_PAGE_SIZE });
        setError(null);
      })
      .catch((e: Error) => { if (active) { setError(e.message || "Query failed."); setData(null); } })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [backend, name, primaryKey, page]);

  if (error) return <Text fontSize="sm" color="fg.error" fontFamily="mono" whiteSpace="pre-wrap">{error}</Text>;
  if (!data) return <Text fontSize="sm" color="fg.muted">{loading ? "Loading…" : ""}</Text>;
  if (data.rows.length === 0 && page === 0) return <Text fontSize="sm" color="fg.muted">No rows.</Text>;

  return (
    <Box>
      <Box overflow="auto" borderWidth="1px" borderColor="border" borderRadius="xl">
        <Box as="table" width="100%" fontFamily="mono" fontSize="xs" css={{ borderCollapse: "collapse" }}>
          <Box as="thead"><Box as="tr" bg="bg.subtle" color="fg.muted">
            {data.columns.map((c) => <Box as="th" key={c} textAlign="left" px="3" py="2" borderBottomWidth="1px" borderColor="border">{c}</Box>)}
          </Box></Box>
          <Box as="tbody">
            {data.rows.map((row, ri) => <Box as="tr" key={ri}>
              {row.map((cell, ci) => <Box as="td" key={ci} px="3" py="2" borderBottomWidth="1px" borderColor="border" maxW="320px" truncate>{show(cell)}</Box>)}
            </Box>)}
          </Box>
        </Box>
      </Box>
      <HStack justify="flex-end" pt="3" gap="3">
        <Text fontSize="xs" color="fg.muted">Page {page + 1}</Text>
        <Button size="xs" variant="outline" disabled={page === 0 || loading} onClick={() => { setPage((p) => p - 1); }}><ChevronLeft size={12} /> Prev</Button>
        <Button size="xs" variant="outline" disabled={!data.hasNext || loading} onClick={() => { setPage((p) => p + 1); }}>Next <ChevronRight size={12} /></Button>
      </HStack>
    </Box>
  );
}
