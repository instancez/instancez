import { useEffect, useRef, useState } from "react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { ChevronLeft, ChevronRight, HardDrive, Upload as UploadIcon } from "lucide-react";
import { useDialog } from "./Dialog";
import { EmptyState } from "./EmptyState";
import { Button } from "./ui";
import { ObjectTable, type Action } from "./ObjectTable";
import { useBackend } from "../console/BackendContext";
import type { StorageFolder, StorageListResult } from "../lib/types";

export function ObjectBrowser({ bucket }: { bucket: string }) {
  const backend = useBackend();
  const dialog = useDialog();
  const fileRef = useRef<HTMLInputElement>(null);

  const [prefix, setPrefix] = useState("");
  // cursors[i] is the cursor that fetches page i; page 0 has none.
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]);
  const [data, setData] = useState<StorageListResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const page = cursors.length - 1;

  useEffect(() => {
    let active = true;
    setLoading(true);
    setData(null);
    setError(null);
    backend.listObjects(bucket, prefix, cursors[page])
      .then((d) => {
        if (!active) return;
        // Last item on a later page was removed: step back.
        if (page > 0 && !d.folders.length && !d.objects.length) setCursors((c) => c.slice(0, -1));
        else setData(d);
      })
      .catch((e: Error) => { if (active) setError(e.message || "Couldn't load objects."); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [backend, bucket, prefix, cursors, page, reloadKey]);

  const reload = () => { setReloadKey((k) => k + 1); };
  const goPrefix = (p: string) => { setCursors([undefined]); setPrefix(p); };

  const segments = prefix.split("/").filter(Boolean);
  const goTo = (i: number) => { goPrefix(i < 0 ? "" : segments.slice(0, i + 1).join("/") + "/"); };
  const openFolder = (f: StorageFolder) => { goPrefix(`${prefix}${f.name}/`); };
  const next = () => { if (data?.next_cursor) setCursors((c) => [...c, data.next_cursor]); };

  const onUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || []);
    e.target.value = "";
    try {
      for (const f of files) await backend.uploadObject(bucket, prefix + f.name, f);
    } catch (err) {
      await dialog.alert("Upload failed", { message: (err as Error).message });
    }
    if (files.length) { if (page > 0) setCursors([undefined]); else reload(); }
  };

  const handleAction = async (a: Action, o: { name: string }) => {
    const key = prefix + o.name;
    try {
      if (a === "download" || a === "copyUrl") {
        const { signedURL } = await backend.signObjectUrl(bucket, key);
        // Signed URLs can serve user-uploaded HTML, so isolate the tab.
        if (a === "download") window.open(signedURL, "_blank", "noopener,noreferrer");
        else await navigator.clipboard.writeText(signedURL);
        return;
      }
      if (a === "rename") {
        const name = await dialog.prompt("Rename to:", { defaultValue: o.name });
        if (!name?.trim() || name === o.name) return;
        await backend.moveObject(bucket, key, prefix + name.trim());
        reload();
        return;
      }
      if (!(await dialog.confirm(`Delete ${o.name}?`, { destructive: true }))) return;
      await backend.deleteObjects(bucket, [key]);
      reload();
    } catch (err) {
      await dialog.alert("Action failed", { message: (err as Error).message });
    }
  };

  return (
    <VStack align="stretch" gap="0">
      <HStack justify="space-between" px="1" pb="3" gap="3">
        <HStack gap="1" fontSize="sm" minW="0" flexWrap="wrap">
          <Box as="button" onClick={() => { goTo(-1); }} fontFamily="mono" fontWeight="medium"
            color={segments.length ? "fg.muted" : "fg"} _hover={{ color: "fg" }} cursor="pointer">{bucket}</Box>
          {segments.map((s, i) => (
            <HStack key={i} gap="1">
              <ChevronRight size={13} />
              <Box as="button" onClick={() => { goTo(i); }} fontFamily="mono"
                color={i === segments.length - 1 ? "fg" : "fg.muted"} _hover={{ color: "fg" }} cursor="pointer">{s}</Box>
            </HStack>
          ))}
        </HStack>
        <Button size="sm" onClick={() => fileRef.current?.click()}><UploadIcon size={14} /> Upload</Button>
        <input ref={fileRef} type="file" multiple hidden onChange={(e) => void onUpload(e)} />
      </HStack>

      {error ? (
        <Text px="1" py="4" fontSize="sm" color="fg.error">{error}</Text>
      ) : loading && !data ? (
        <Text px="1" py="4" fontSize="sm" color="fg.muted">Loading…</Text>
      ) : data && (data.folders.length || data.objects.length) ? (
        <Box borderWidth="1px" borderColor="border" borderRadius="xl">
          <ObjectTable folders={data.folders} objects={data.objects} onOpenFolder={openFolder} onAction={(a, o) => void handleAction(a, o)} />
        </Box>
      ) : (
        <EmptyState icon={HardDrive} title="Empty" description="No files here yet. Upload one to get started." />
      )}

      {(page > 0 || data?.has_next) && (
        <HStack justify="flex-end" pt="3" gap="3">
          <Text fontSize="xs" color="fg.muted">Page {page + 1}</Text>
          <Button size="xs" variant="outline" disabled={page === 0 || loading}
            onClick={() => { setCursors((c) => c.slice(0, -1)); }}><ChevronLeft size={12} /> Prev</Button>
          <Button size="xs" variant="outline" disabled={!data?.has_next || loading} onClick={next}>
            Next <ChevronRight size={12} />
          </Button>
        </HStack>
      )}
    </VStack>
  );
}
