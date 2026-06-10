import { useNavigate } from "react-router-dom";
import { Plus, Table2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";

export function Tables() {
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();

  if (!config) return null;

  const tables = Object.entries(config.tables || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  async function addTable() {
    const name = await dialog.prompt("Table name:");
    if (!name?.trim()) return;
    const tableName = name.trim().toLowerCase().replace(/\s+/g, "_");

    const updated = {
      ...config!,
      tables: {
        ...config!.tables,
        [tableName]: {
          fields: {
            id: { type: "bigserial", primary_key: true },
            created_at: { type: "timestamptz", default: "now()" },
          },
          indexes: [],
          rls: [],
          searchable: [],
          search_config: "english",
        },
      },
    };

    const ok = await save(updated);
    if (ok) navigate(`/tables/${tableName}`);
  }

  return (
    <div>
      <PageHeader
        title="Tables"
        description={`${tables.length} table${tables.length !== 1 ? "s" : ""} defined`}
        actions={
          <button
            onClick={addTable}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-background text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
          >
            <Plus size={14} />
            Add Table
          </button>
        }
      />

      <div className="px-8">
        {tables.length === 0 ? (
          <EmptyState
            icon={Table2}
            title="No tables yet"
            description="Define your first table to get started."
            action={
              <button
                onClick={addTable}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-background text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
              >
                <Plus size={14} />
                Add Table
              </button>
            }
          />
        ) : (
          <div className="space-y-2">
            {tables.map(([name, table]) => {
              const fieldCount = Object.keys(table.fields || {}).length;
              const rlsCount = (table.rls || []).length;
              const indexCount = (table.indexes || []).length;

              return (
                <button
                  key={name}
                  onClick={() => navigate(`/tables/${name}`)}
                  className="w-full flex items-center justify-between px-5 py-3.5 rounded-lg border border-border bg-surface hover:bg-surface-hover hover:border-border-hover transition-colors cursor-pointer text-left group"
                >
                  <div className="flex items-center gap-3">
                    <Table2
                      size={16}
                      className="text-muted-foreground group-hover:text-foreground transition-colors"
                    />
                    <span className="text-sm font-mono font-medium text-foreground">
                      {name}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <StatusBadge variant="muted">
                      {fieldCount} field{fieldCount !== 1 ? "s" : ""}
                    </StatusBadge>
                    {indexCount > 0 && (
                      <StatusBadge variant="muted">
                        {indexCount} index{indexCount !== 1 ? "es" : ""}
                      </StatusBadge>
                    )}
                    {rlsCount > 0 && (
                      <StatusBadge variant="info">
                        {rlsCount} RLS
                      </StatusBadge>
                    )}
                  </div>
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
