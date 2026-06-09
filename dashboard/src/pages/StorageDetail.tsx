import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { ArrowLeft, Trash2, Plus } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { TagInput } from "../components/TagInput";
import { CodeEditor } from "../components/CodeEditor";
import { Toggle } from "../components/Toggle";
import { RLS_OPERATIONS } from "../lib/utils";
import type { Bucket } from "../lib/types";

export function StorageDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [bucket, setBucket] = useState<Bucket | null>(null);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (config && name && config.storage[name]) {
      setBucket(structuredClone(config.storage[name]!));
      setDirty(false);
    }
  }, [config, name]);

  function updateBucket(updater: (prev: Bucket) => Bucket) {
    setBucket((prev) => {
      if (!prev) return prev;
      setDirty(true);
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
    setDirty(false);
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

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description="Storage bucket configuration"
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate("/storage")}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
            >
              <ArrowLeft size={14} />
              Back
            </button>
            <button
              onClick={deleteBucket}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-destructive/30 text-sm text-destructive hover:bg-destructive/10 transition-colors cursor-pointer"
            >
              <Trash2 size={14} />
              Delete
            </button>
          </div>
        }
      />

      <div className="px-8 space-y-6 max-w-2xl">
        <div>
          <label className="block text-sm font-medium text-foreground mb-1">Max File Size</label>
          <input
            type="text"
            value={bucket.max_size}
            onChange={(e) => updateBucket((b) => ({ ...b, max_size: e.target.value }))}
            placeholder="5MB"
            className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-foreground mb-1">Allowed MIME Types</label>
          <TagInput
            value={bucket.types || []}
            onChange={(types) => updateBucket((b) => ({ ...b, types }))}
            placeholder="e.g. image/*, application/pdf"
            suggestions={["image/*", "image/png", "image/jpeg", "image/webp", "application/pdf", "video/*", "audio/*"]}
          />
        </div>

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

        {/* RLS Policies */}
        <div>
          <h2 className="text-sm font-medium text-foreground mb-3">RLS Policies</h2>
          <div className="space-y-3">
            {(bucket.rls || []).map((policy, i) => (
              <div
                key={i}
                className="p-4 rounded-lg border border-border bg-primary space-y-3"
              >
                <div className="flex items-start justify-between">
                  <div className="space-y-3">
                    <div>
                      <label className="block text-xs font-medium text-muted-foreground mb-2">Type</label>
                      <div className="flex gap-1">
                        {(["permissive", "restrictive"] as const).map((t) => (
                          <button
                            key={t}
                            type="button"
                            onClick={() =>
                              updateBucket((b) => {
                                const rls = [...(b.rls || [])];
                                rls[i] = { ...rls[i]!, type: t };
                                return { ...b, rls };
                              })
                            }
                            className={`px-2.5 py-1 rounded text-xs font-medium transition-colors cursor-pointer ${
                              (policy.type || "permissive") === t
                                ? t === "restrictive"
                                  ? "bg-amber-500/15 text-amber-400 border border-amber-500/30"
                                  : "bg-accent/15 text-accent border border-accent/30"
                                : "border border-border text-muted-foreground hover:text-foreground hover:bg-surface-hover"
                            }`}
                          >
                            {t}
                          </button>
                        ))}
                      </div>
                    </div>
                    <div className="flex gap-2">
                      {RLS_OPERATIONS.map((op) => (
                        <label
                          key={op}
                          className="flex items-center gap-1.5 text-xs text-foreground cursor-pointer"
                        >
                          <input
                            type="checkbox"
                            checked={(policy.operations || []).includes(op)}
                            onChange={(e) =>
                              updateBucket((b) => {
                                const rls = [...(b.rls || [])];
                                const ops = e.target.checked
                                  ? [...(rls[i]!.operations || []), op]
                                  : (rls[i]!.operations || []).filter((o) => o !== op);
                                rls[i] = { ...rls[i]!, operations: ops };
                                return { ...b, rls };
                              })
                            }
                            className="rounded border-border"
                          />
                          {op}
                        </label>
                      ))}
                    </div>
                  </div>
                  <button
                    onClick={() =>
                      updateBucket((b) => ({
                        ...b,
                        rls: (b.rls || []).filter((_, j) => j !== i),
                      }))
                    }
                    className="p-1.5 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
                <CodeEditor
                  value={policy.check || ""}
                  onChange={(val) =>
                    updateBucket((b) => {
                      const rls = [...(b.rls || [])];
                      rls[i] = { ...rls[i]!, check: val };
                      return { ...b, rls };
                    })
                  }
                  minHeight="60px"
                />
              </div>
            ))}
            <button
              onClick={() =>
                updateBucket((b) => ({
                  ...b,
                  rls: [...(b.rls || []), { operations: ["select"], check: "" }],
                }))
              }
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-dashed border-border text-sm text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
            >
              <Plus size={14} />
              Add RLS Policy
            </button>
          </div>
        </div>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
