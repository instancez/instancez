import { useNavigate } from "react-router-dom";
import { Plus, HardDrive } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";

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
    if (ok) navigate(`/storage/${bucketName}`);
  }

  return (
    <div>
      <PageHeader
        title="Storage"
        description={`${buckets.length} bucket${buckets.length !== 1 ? "s" : ""} configured`}
        actions={
          <button
            onClick={addBucket}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
          >
            <Plus size={14} />
            Add Bucket
          </button>
        }
      />

      <div className="px-8">
        {buckets.length === 0 ? (
          <EmptyState
            icon={HardDrive}
            title="No storage buckets"
            description="Create a bucket to start managing file uploads."
            action={
              <button
                onClick={addBucket}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
              >
                <Plus size={14} />
                Add Bucket
              </button>
            }
          />
        ) : (
          <div className="space-y-2">
            {buckets.map(([name, bucket]) => (
              <button
                key={name}
                onClick={() => navigate(`/storage/${name}`)}
                className="w-full flex items-center justify-between px-5 py-3.5 rounded-lg border border-border bg-surface hover:bg-surface-hover hover:border-border-hover transition-colors cursor-pointer text-left group"
              >
                <div className="flex items-center gap-3">
                  <HardDrive
                    size={16}
                    className="text-muted-foreground group-hover:text-foreground transition-colors"
                  />
                  <span className="text-sm font-mono font-medium text-foreground">
                    {name}
                  </span>
                </div>
                <div className="flex items-center gap-2">
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
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
