import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, HardDrive, Settings2, Upload as UploadIcon, ChevronRight, ArrowLeft } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { Button } from "../components/ui";
import { ObjectTable } from "../components/ObjectTable";
import { useBackend } from "../console/BackendContext";
import type { StorageFolder, StorageListResult } from "../lib/types";

export function Storage() {
  const backend = useBackend();
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();
  const canWriteConfig = backend.capabilities.canWriteConfig;

  const [bucket, setBucket] = useState<string | null>(null);
  const [prefix, setPrefix] = useState("");
  const [data, setData] = useState<StorageListResult | null>(null);
  const [loading, setLoading] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = useCallback(async (b: string, p: string) => {
    setLoading(true);
    try { setData(await backend.listObjects(b, p)); }
    catch { setData(null); }
    finally { setLoading(false); }
  }, [backend]);

  useEffect(() => {
    if (bucket != null) void load(bucket, prefix);
  }, [bucket, prefix, load]);

  if (!config) return null;

  const buckets = Object.entries(config.storage).sort(([a], [b]) => a.localeCompare(b));

  const addBucket = async () => {
    const name = await dialog.prompt("Bucket name:");
    if (!name?.trim()) return;
    const bucketName = name.trim().toLowerCase().replace(/\s+/g, "_");
    const updated = {
      ...config,
      storage: { ...config.storage, [bucketName]: { max_size: "5MB", types: ["image/*"], public: false, rls: [] } },
    };
    try {
      await save(updated);
    } catch (err) {
      await dialog.alert("Couldn't create bucket", { message: (err as Error).message });
    }
  }

  // ── Bucket list ──────────────────────────────────────────────────────────
  if (bucket == null) {
    const addButton = canWriteConfig ? (
      <Button onClick={() => void addBucket()}><Plus size={14} /> Add Bucket</Button>
    ) : null;
    return (
      <Box pb="8">
        <HStack justify="space-between" gap="4" pb="6">
          <Text fontSize="sm" color="fg.muted">{buckets.length} bucket{buckets.length !== 1 ? "s" : ""} configured</Text>
          {addButton}
        </HStack>
        {buckets.length === 0 ? (
          <EmptyState icon={HardDrive} title="No storage buckets"
            description="Create a bucket to start managing file uploads." action={addButton} />
        ) : (
          <VStack gap="2" align="stretch">
            {buckets.map(([name]) => (
              <HStack key={name} justify="space-between" px="5" py="3.5" borderRadius="xl" borderWidth="1px"
                bg="bg" _hover={{ bg: "bg.subtle" }} transition="colors">
                <HStack gap="3" minW="0" flex="1" cursor="pointer" onClick={() => { setPrefix(""); setBucket(name); }}>
                  <Box as={HardDrive} boxSize="4" color="fg.muted" flexShrink="0" />
                  <Text fontSize="sm" fontFamily="mono" fontWeight="medium" truncate>{name}</Text>
                </HStack>
                <Box as="button" aria-label="Bucket settings" p="1.5" borderRadius="md" color="fg.muted"
                  _hover={{ bg: "bg.muted", color: "fg" }} cursor="pointer"
                  onClick={() => { void navigate(name, { relative: "path" }); }}>
                  <Settings2 size={15} />
                </Box>
              </HStack>
            ))}
          </VStack>
        )}
      </Box>
    );
  }

  // ── File explorer ────────────────────────────────────────────────────────
  const segments = prefix.split("/").filter(Boolean);
  const openFolder = (f: StorageFolder) => { setPrefix(`${prefix}${f.name}/`); };
  const goTo = (i: number) => { setPrefix(i < 0 ? "" : segments.slice(0, i + 1).join("/") + "/"); };

  const reload = () => { void load(bucket, prefix); };

  const onUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || []);
    e.target.value = "";
    try {
      for (const f of files) await backend.uploadObject(bucket, prefix + f.name, f);
    } catch (err) {
      await dialog.alert("Upload failed", { message: (err as Error).message });
    }
    if (files.length) reload();
  }

  const handleAction = async (a: "download" | "copyUrl" | "rename" | "delete", o: { name: string }) => {
    const key = prefix + o.name;
    try {
      if (a === "download" || a === "copyUrl") {
        const { signedURL } = await backend.signObjectUrl(bucket, key);
        // window.open (unlike <a target=_blank>) does not imply noopener; a signed
        // URL can resolve to user-uploaded HTML, so isolate the opened tab.
        if (a === "download") window.open(signedURL, "_blank", "noopener,noreferrer");
        else await navigator.clipboard.writeText(signedURL);
        return;
      }
      if (a === "rename") {
        const next = await dialog.prompt("Rename to:", { defaultValue: o.name });
        if (!next?.trim() || next === o.name) return;
        await backend.moveObject(bucket, key, prefix + next.trim());
        reload();
        return;
      }
      // a is narrowed to "delete" here (download/copyUrl/rename returned above).
      const ok = await dialog.confirm(`Delete ${o.name}?`, { destructive: true });
      if (!ok) return;
      await backend.deleteObjects(bucket, [key]);
      reload();
    } catch (err) {
      await dialog.alert("Action failed", { message: (err as Error).message });
    }
  }

  return (
    <VStack align="stretch" gap="0">
      <HStack justify="space-between" px="1" pb="3" gap="3">
        <HStack gap="1" fontSize="sm" minW="0" flexWrap="wrap">
          <Box as="button" onClick={() => { setBucket(null); }} color="fg.muted" _hover={{ color: "fg" }} cursor="pointer">
            <HStack gap="1"><ArrowLeft size={14} /> Buckets</HStack>
          </Box>
          <ChevronRight size={13} />
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

      {loading && !data ? (
        <Text px="1" py="4" fontSize="sm" color="fg.muted">Loading…</Text>
      ) : data && (data.folders.length || data.objects.length) ? (
        <Box borderWidth="1px" borderColor="border" borderRadius="xl" overflow="hidden">
          <ObjectTable folders={data.folders} objects={data.objects} onOpenFolder={openFolder} onAction={(a, o) => void handleAction(a, o)} />
        </Box>
      ) : (
        <EmptyState icon={HardDrive} title="Empty" description="No files here yet. Upload one to get started." />
      )}
      {data?.has_next && (
        <Text px="1" pt="3" fontSize="xs" color="fg.muted">First 100 shown.</Text>
      )}
    </VStack>
  );
}
