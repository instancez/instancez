import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, HardDrive } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import { formatBytes } from "../lib/utils";
import type { StatsResponse } from "../lib/types";

export function Storage() {
  const backend = useBackend();
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();
  const canWriteConfig = backend.capabilities.canWriteConfig;
  const hasStats = backend.capabilities.hasStats;
  const [stats, setStats] = useState<StatsResponse | null>(null);

  useEffect(() => {
    if (!hasStats) return;
    backend.getStats().then(setStats).catch(() => { setStats(null); });
  }, [backend, hasStats]);

  if (!config) return null;

  const usageByBucket = new Map(Object.entries(stats?.storage ?? {}));

  const buckets = Object.keys(config.storage).sort((a, b) => a.localeCompare(b));

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
  };

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
          {buckets.map((name) => {
            const usage = usageByBucket.get(name);
            return (
              <ListRow
                key={name}
                icon={HardDrive}
                title={name}
                onClick={() => navigate(name, { relative: "path" })}
                badges={usage && <StatusBadge variant="muted">{formatBytes(usage.total_bytes)}</StatusBadge>}
              />
            );
          })}
        </VStack>
      )}
    </Box>
  );
}
