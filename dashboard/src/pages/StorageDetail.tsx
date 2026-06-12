import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { Plus, Settings2, ShieldCheck } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
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
    if (ok) navigate("/storage");
  }

  if (!config || !bucket || !name) {
    return (
      <div className="p-8">
        <p className="text-sm text-muted-foreground">Bucket not found.</p>
      </div>
    );
  }

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty = !jsonEqual(bucket, config.storage[name] ?? null);

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description="Storage bucket configuration"
        backTo="/storage"
        onDelete={deleteBucket}
      />

      <div className="px-8 pb-8 space-y-6 max-w-2xl">
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
                <span className="text-xs text-muted-foreground">
                  (allows unauthenticated downloads)
                </span>
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
            <p className="text-sm text-muted-foreground">No policies defined.</p>
          ) : (
            <div className="space-y-3">
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
            </div>
          )}
        </Section>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
