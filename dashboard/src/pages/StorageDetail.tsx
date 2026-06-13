import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { Plus, Settings2, ShieldCheck } from "lucide-react";
import { Box, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { DetailToolbar } from "../components/DetailToolbar";
import { SaveBar } from "../components/SaveBar";
import { TagInput } from "../components/TagInput";
import { Toggle } from "../components/Toggle";
import { RlsPolicyCard } from "../components/RlsPolicyCard";
import { Button, Field, Input, Section } from "../components/ui";
import type { Bucket } from "../lib/types";

export function StorageDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [bucket, setBucket] = useState<Bucket | null>(null);

  useEffect(() => {
    if (config && name && config.storage[name]) {
      setBucket(structuredClone(config.storage[name]!));
    }
  }, [config, name]);

  function updateBucket(updater: (prev: Bucket) => Bucket) {
    setBucket((prev) => {
      if (!prev) return prev;
      return updater(prev);
    });
  }

  async function handleSave() {
    if (!config || !bucket || !name) return;
    const updated = {
      ...config,
      storage: { ...config.storage, [name]: bucket },
    };
    await save(updated);
  }

  async function deleteBucket() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete bucket "${name}"?`, { message: "This will delete the bucket and all uploaded files.", confirmText: name }))) return;
    const { [name]: _, ...rest } = config.storage;
    const updated = { ...config, storage: rest };
    const ok = await save(updated);
    if (ok) navigate("..", { relative: "path" });
  }

  if (!config || !bucket || !name) {
    return (
      <Box p="8">
        <Text fontSize="sm" color="fg.muted">Bucket not found.</Text>
      </Box>
    );
  }

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty = !jsonEqual(bucket, config.storage[name] ?? null);

  return (
    <Box pb="20">
      <DetailToolbar backLabel="Storage" onDelete={deleteBucket} />
      <VStack pb="8" gap="6" maxW="2xl" align="stretch">
        <Section
          title="Bucket Settings"
          icon={Settings2}
        >
          <Field label="Max File Size">
            <Input
              mono
              value={bucket.max_size}
              onChange={(e) => updateBucket((b) => ({ ...b, max_size: e.target.value }))}
              placeholder="5MB"
            />
          </Field>

          <Field label="Allowed MIME Types">
            <TagInput
              value={bucket.types || []}
              onChange={(types) => updateBucket((b) => ({ ...b, types }))}
              placeholder="e.g. image/*, application/pdf"
              suggestions={["image/*", "image/png", "image/jpeg", "image/webp", "application/pdf", "video/*", "audio/*"]}
            />
          </Field>

          <Toggle
            checked={bucket.public}
            onChange={(v) => updateBucket((b) => ({ ...b, public: v }))}
            label={
              <>
                Public bucket{" "}
                <Text as="span" fontSize="xs" color="fg.muted">
                  (allows unauthenticated downloads)
                </Text>
              </>
            }
          />
        </Section>

        <Section
          title="RLS Policies"
          icon={ShieldCheck}
          actions={
            <Button
              variant="dashed"
              size="sm"
              onClick={() =>
                updateBucket((b) => ({
                  ...b,
                  rls: [...(b.rls || []), { operations: ["select"], check: "" }],
                }))
              }
            >
              <Plus size={14} />
              Add RLS Policy
            </Button>
          }
        >
          {(bucket.rls || []).length === 0 ? (
            <Text fontSize="sm" color="fg.muted">No policies defined.</Text>
          ) : (
            <VStack gap="3" align="stretch">
              {(bucket.rls || []).map((policy, i) => (
                <RlsPolicyCard
                  key={i}
                  policy={policy}
                  onChange={(p) =>
                    updateBucket((b) => {
                      const rls = [...(b.rls || [])];
                      rls[i] = p;
                      return { ...b, rls };
                    })
                  }
                  onDelete={() =>
                    updateBucket((b) => ({
                      ...b,
                      rls: (b.rls || []).filter((_, j) => j !== i),
                    }))
                  }
                />
              ))}
            </VStack>
          )}
        </Section>
      </VStack>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </Box>
  );
}
