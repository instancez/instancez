import { useNavigate } from "react-router-dom";
import { Plus, Table2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow } from "../components/ui";

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
          fields: [
            { name: "id", type: "bigserial", primary_key: true },
            { name: "created_at", type: "timestamptz", default: "now()" },
          ],
          indexes: [],
          rls: [],
        },
      },
    };

    const ok = await save(updated);
    if (ok) navigate(tableName, { relative: "path" });
  }

  const addButton = (
    <Button onClick={addTable}>
      <Plus size={14} />
      Add Table
    </Button>
  );

  return (
    <div>
      <div className="pb-8">
        <div className="flex items-center justify-between gap-4 pb-6">
          <p className="text-sm text-muted-foreground">
            {tables.length} table{tables.length !== 1 ? "s" : ""} defined
          </p>
          {addButton}
        </div>
        {tables.length === 0 ? (
          <EmptyState
            icon={Table2}
            title="No tables yet"
            description="Define your first table to get started."
            action={addButton}
          />
        ) : (
          <div className="space-y-2">
            {tables.map(([name, table]) => {
              const fieldCount = (table.fields || []).length;
              const rlsCount = (table.rls || []).length;
              const indexCount = (table.indexes || []).length;

              return (
                <ListRow
                  key={name}
                  icon={Table2}
                  title={name}
                  onClick={() => navigate(name, { relative: "path" })}
                  badges={
                    <>
                      <StatusBadge variant="muted">
                        {fieldCount} field{fieldCount !== 1 ? "s" : ""}
                      </StatusBadge>
                      {indexCount > 0 && (
                        <StatusBadge variant="muted">
                          {indexCount} index{indexCount !== 1 ? "es" : ""}
                        </StatusBadge>
                      )}
                      {rlsCount > 0 && (
                        <StatusBadge variant="info">{rlsCount} RLS</StatusBadge>
                      )}
                    </>
                  }
                />
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
