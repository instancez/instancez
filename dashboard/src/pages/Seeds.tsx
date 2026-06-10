import { useState, useEffect } from "react";
import { Plus, Trash2, Database } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { EmptyState } from "../components/EmptyState";
import { Toggle } from "../components/Toggle";

export function Seeds() {
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [seeds, setSeeds] = useState<Record<string, Record<string, unknown>[]>>({});
  const [selectedTable, setSelectedTable] = useState<string>("");
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (config) {
      setSeeds(structuredClone(config.seeds || {}));
      setDirty(false);
      // Auto-select first table with seeds
      const tables = Object.keys(config.seeds || {});
      if (tables.length > 0 && !selectedTable) {
        setSelectedTable(tables[0]!);
      }
    }
  }, [config]);

  async function handleSave() {
    if (!config) return;
    await save({ ...config, seeds });
    setDirty(false);
  }

  async function addSeedTable() {
    if (!config) return;
    const availableTables = Object.keys(config.tables || {}).filter(
      (t) => !seeds[t]
    );
    if (availableTables.length === 0) {
      await dialog.alert("All tables already have seeds.");
      return;
    }
    const name = await dialog.select("Add seeds for table:", availableTables);
    if (!name) return;
    setSeeds((prev) => ({ ...prev, [name]: [] }));
    setSelectedTable(name);
    setDirty(true);
  }

  if (!config) return null;

  const tableNames = Object.keys(seeds);
  const currentSeeds = selectedTable ? seeds[selectedTable] || [] : [];
  const tableFields = selectedTable
    ? Object.entries(config.tables[selectedTable]?.fields || {})
    : [];

  return (
    <div className="pb-20">
      <PageHeader
        title="Seeds"
        description="Manage seed data for your tables"
        actions={
          <button
            onClick={addSeedTable}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-background text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
          >
            <Plus size={14} />
            Add Table Seeds
          </button>
        }
      />

      <div className="px-8">
        {tableNames.length === 0 ? (
          <EmptyState
            icon={Database}
            title="No seeds defined"
            description="Add seed data to pre-populate your tables."
            action={
              <button
                onClick={addSeedTable}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-background text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
              >
                <Plus size={14} />
                Add Table Seeds
              </button>
            }
          />
        ) : (
          <div className="space-y-4">
            {/* Table Selector */}
            <div className="flex items-center gap-3">
              <select
                value={selectedTable}
                onChange={(e) => setSelectedTable(e.target.value)}
                className="px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground cursor-pointer focus:outline-none focus:border-ring transition-colors"
              >
                {tableNames.map((t) => (
                  <option key={t} value={t}>
                    {t} ({(seeds[t] || []).length} rows)
                  </option>
                ))}
              </select>
              <button
                onClick={async () => {
                  if (!selectedTable) return;
                  if (!(await dialog.confirm(`Remove all seeds for "${selectedTable}"?`, { confirmText: selectedTable }))) return;
                  setSeeds((prev) => {
                    const { [selectedTable]: _, ...rest } = prev;
                    return rest;
                  });
                  setSelectedTable(tableNames.find((t) => t !== selectedTable) || "");
                  setDirty(true);
                }}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-destructive/30 text-xs text-destructive hover:bg-destructive/10 transition-colors cursor-pointer"
              >
                <Trash2 size={12} />
                Remove Table
              </button>
            </div>

            {/* Seed Grid */}
            {selectedTable && tableFields.length > 0 && (
              <div className="overflow-x-auto">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border">
                      <th className="w-10 px-2 py-2 text-xs font-medium text-muted-foreground">#</th>
                      {tableFields.map(([fieldName, field]) => (
                        <th
                          key={fieldName}
                          className="text-left px-3 py-2 text-xs font-medium text-muted-foreground"
                        >
                          <span className="font-mono">{fieldName}</span>
                          <span className="ml-1 text-muted-foreground/50">
                            {field.type}
                          </span>
                        </th>
                      ))}
                      <th className="w-10" />
                    </tr>
                  </thead>
                  <tbody>
                    {currentSeeds.map((row, rowIdx) => (
                      <tr
                        key={rowIdx}
                        className="border-b border-border/50 hover:bg-surface-hover/30"
                      >
                        <td className="px-2 py-1.5 text-xs text-muted-foreground tabular-nums">
                          {rowIdx + 1}
                        </td>
                        {tableFields.map(([fieldName, field]) => (
                          <td key={fieldName} className="px-3 py-1.5">
                            {field.type === "boolean" ? (
                              <Toggle
                                aria-label={fieldName}
                                checked={!!row[fieldName]}
                                onChange={(v) => {
                                  const updated = [...currentSeeds];
                                  updated[rowIdx] = {
                                    ...updated[rowIdx]!,
                                    [fieldName]: v,
                                  };
                                  setSeeds((prev) => ({
                                    ...prev,
                                    [selectedTable]: updated,
                                  }));
                                  setDirty(true);
                                }}
                              />
                            ) : field.enum && field.enum.length > 0 ? (
                              <select
                                value={String(row[fieldName] ?? "")}
                                onChange={(e) => {
                                  const updated = [...currentSeeds];
                                  updated[rowIdx] = {
                                    ...updated[rowIdx]!,
                                    [fieldName]: e.target.value,
                                  };
                                  setSeeds((prev) => ({
                                    ...prev,
                                    [selectedTable]: updated,
                                  }));
                                  setDirty(true);
                                }}
                                className="w-full px-2 py-0.5 rounded border border-border bg-input text-xs font-mono text-foreground cursor-pointer focus:outline-none focus:border-ring"
                              >
                                <option value="">—</option>
                                {field.enum.map((v) => (
                                  <option key={v} value={v}>{v}</option>
                                ))}
                              </select>
                            ) : (
                              <input
                                type={
                                  field.type === "integer" || field.type === "bigint"
                                    ? "number"
                                    : field.type?.includes("password")
                                      ? "password"
                                      : "text"
                                }
                                value={String(row[fieldName] ?? "")}
                                onChange={(e) => {
                                  const updated = [...currentSeeds];
                                  updated[rowIdx] = {
                                    ...updated[rowIdx]!,
                                    [fieldName]: e.target.value,
                                  };
                                  setSeeds((prev) => ({
                                    ...prev,
                                    [selectedTable]: updated,
                                  }));
                                  setDirty(true);
                                }}
                                placeholder="—"
                                className="w-full px-2 py-0.5 rounded border border-border bg-input text-xs font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
                              />
                            )}
                          </td>
                        ))}
                        <td className="px-1 py-1.5">
                          <button
                            onClick={async () => {
                              if (!(await dialog.confirm(`Delete row ${rowIdx + 1}?`))) return;
                              const updated = currentSeeds.filter(
                                (_, i) => i !== rowIdx
                              );
                              setSeeds((prev) => ({
                                ...prev,
                                [selectedTable]: updated,
                              }));
                              setDirty(true);
                            }}
                            className="p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                          >
                            <Trash2 size={12} />
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>

                <button
                  onClick={() => {
                    const newRow: Record<string, unknown> = {};
                    tableFields.forEach(([fieldName, field]) => {
                      if (field.default !== undefined && field.default !== null) {
                        newRow[fieldName] = "";
                      }
                    });
                    setSeeds((prev) => ({
                      ...prev,
                      [selectedTable]: [...(prev[selectedTable] || []), newRow],
                    }));
                    setDirty(true);
                  }}
                  className="mt-3 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-dashed border-border text-sm text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
                >
                  <Plus size={14} />
                  Add Row
                </button>
              </div>
            )}
          </div>
        )}
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
