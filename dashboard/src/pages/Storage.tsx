import { useNavigate } from "react-router-dom";
import { Plus, HardDrive } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow } from "../components/ui";

export function Storage() {
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();

  if (!config) return null;

  const buckets = Object.entries(config.storage || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  async function addBucket() {
    const name = await dialog.prompt("Bucket name:");
    if (!name?.trim()) return;
    const bucketName = name.trim().toLowerCase().replace(/\s+/g, "_");

    const updated = {
      ...config!,
      storage: {
        ...config!.storage,
        [bucketName]: {
          max_size: "5MB",
          types: ["image/*"],
          public: false,
          rls: [],
        },
      },
    };

    const ok = await save(updated);
    if (ok) navigate(bucketName, { relative: "path" });
  }

  const addButton = (
    <Button onClick={addBucket}>
      <Plus size={14} />
      Add Bucket
    </Button>
  );

  return (
    <Box>
      <Box pb="8">
        <HStack justify="space-between" gap="4" pb="6">
          <Text fontSize="sm" color="fg.muted">
            {buckets.length} bucket{buckets.length !== 1 ? "s" : ""} configured
          </Text>
          {addButton}
        </HStack>
        {buckets.length === 0 ? (
          <EmptyState
            icon={HardDrive}
            title="No storage buckets"
            description="Create a bucket to start managing file uploads."
            action={addButton}
          />
        ) : (
          <VStack gap="2" align="stretch">
            {buckets.map(([name, bucket]) => (
              <ListRow
                key={name}
                icon={HardDrive}
                title={name}
                onClick={() => navigate(name, { relative: "path" })}
                badges={
                  <>
                    <StatusBadge variant="muted">{bucket.max_size}</StatusBadge>
                    {bucket.types.length > 0 && (
                      <StatusBadge variant="muted">
                        {bucket.types.length} type{bucket.types.length !== 1 ? "s" : ""}
                      </StatusBadge>
                    )}
                    {bucket.public && (
                      <StatusBadge variant="warning">public</StatusBadge>
                    )}
                    {(bucket.rls || []).length > 0 && (
                      <StatusBadge variant="info">
                        {bucket.rls.length} RLS
                      </StatusBadge>
                    )}
                  </>
                }
              />
            ))}
          </VStack>
        )}
      </Box>
    </Box>
  );
}
