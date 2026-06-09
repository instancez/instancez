import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect, useCallback } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import {
  ArrowLeft,
  Plus,
  Trash2,
  GripVertical,
  ChevronDown,
  ChevronUp,
} from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { TagInput } from "../components/TagInput";
import { Toggle } from "../components/Toggle";
import { DiffViewer } from "../components/DiffViewer";
import { getConfigDiff } from "../api/client";
import { POSTGRES_TYPES, SQL_DEFAULTS, RLS_OPERATIONS, SEARCH_CONFIGS } from "../lib/utils";
import type { Table, Field, Index, RLSPolicy, DiffResponse } from "../lib/types";

export function TableDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [table, setTable] = useState<Table | null>(null);
  const [seeds, setSeeds] = useState<Record<string, unknown>[]>([]);
  const [dirty, setDirty] = useState(false);
  const [showPreview, setShowPreview] = useState(false);
  const [diff, setDiff] = useState<DiffResponse | null>(null);

  useEffect(() => {
    if (config && name && config.tables[name]) {
      setTable(structuredClone(config.tables[name]!));
      setSeeds(structuredClone(config.seeds?.[name] || []));
      setDirty(false);
    }
  }, [config, name]);

  const updateTable = useCallback(
    (updater: (prev: Table) => Table) => {
      setTable((prev) => {
        if (!prev) return prev;
        const next = updater(prev);
        setDirty(true);
        return next;
      });
    },
    []
  );

  async function handleSave() {
    if (!config || !table || !name) return;
    const updatedSeeds = { ...config.seeds };
    if (seeds.length > 0) {
      updatedSeeds[name] = seeds;
    } else {
      delete updatedSeeds[name];
    }
    const updated = {
      ...config,
      tables: { ...config.tables, [name]: table },
      seeds: updatedSeeds,
    };
    await save(updated);
    setDirty(false);
  }

  async function loadDiff() {
    try {
      const d = await getConfigDiff();
      setDiff(d);
    } catch {
      // ignore
    }
  }

  useEffect(() => {
    if (showPreview) loadDiff();
  }, [showPreview]);

  async function deleteTable() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete table "${name}"?`, { message: "This will drop the table and all its data.", confirmText: name }))) return;
    const { [name]: _, ...rest } = config.tables;
    const updated = { ...config, tables: rest };
    const ok = await save(updated);
    if (ok) navigate("/tables");
  }

  if (!config || !table || !name) {
    return (
      <div className="p-8">
        <p className="text-sm text-muted-foreground">Table not found.</p>
      </div>
    );
  }

  const fieldEntries = Object.entries(table.fields || {});
  const allTableColumns = fieldEntries.map(([n]) => n);

  // FK options: all table.column pairs
  const fkOptions: string[] = [];
  for (const [tName, t] of Object.entries(config.tables)) {
    for (const fName of Object.keys(t.fields || {})) {
      fkOptions.push(`${tName}.${fName}`);
    }
  }
  fkOptions.push("users.id");

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description={`${fieldEntries.length} fields, ${(table.indexes || []).length} indexes, ${(table.rls || []).length} RLS policies`}
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate("/tables")}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
            >
              <ArrowLeft size={14} />
              Back
            </button>
            <button
              onClick={deleteTable}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-destructive/30 text-sm text-destructive hover:bg-destructive/10 transition-colors cursor-pointer"
            >
              <Trash2 size={14} />
              Delete
            </button>
          </div>
        }
      />

      <div className="px-8">
        <Tabs.Root defaultValue="fields">
          <Tabs.List className="flex gap-1 border-b border-border mb-6">
            {["Fields", "Indexes", "RLS", "Search", "Seeds"].map((tab) => (
              <Tabs.Trigger
                key={tab}
                value={tab.toLowerCase()}
                className="px-4 py-2 text-sm font-medium text-muted-foreground data-[state=active]:text-accent data-[state=active]:border-b-2 data-[state=active]:border-accent -mb-px transition-colors cursor-pointer hover:text-foreground"
              >
                {tab}
              </Tabs.Trigger>
            ))}
          </Tabs.List>

          {/* Fields Tab */}
          <Tabs.Content value="fields">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border">
                    <th className="w-8" />
                    <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Name</th>
                    <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Type</th>
                    <th className="text-center px-2 py-2 text-xs font-medium text-muted-foreground">PK</th>
                    <th className="text-center px-2 py-2 text-xs font-medium text-muted-foreground">Required</th>
                    <th className="text-center px-2 py-2 text-xs font-medium text-muted-foreground">Unique</th>
                    <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">Default</th>
                    <th className="text-left px-3 py-2 text-xs font-medium text-muted-foreground">FK</th>
                    <th className="w-10" />
                  </tr>
                </thead>
                <tbody>
                  {fieldEntries.map(([fieldName, field]) => (
                    <FieldRow
                      key={fieldName}
                      name={fieldName}
                      field={field}
                      fkOptions={fkOptions}
                      onChange={(updated) =>
                        updateTable((t) => ({
                          ...t,
                          fields: { ...t.fields, [fieldName]: updated },
                        }))
                      }
                      onRename={(newName) => {
                        updateTable((t) => {
                          const { [fieldName]: f, ...rest } = t.fields;
                          return { ...t, fields: { ...rest, [newName]: f! } };
                        });
                      }}
                      onDelete={async () => {
                        if (!(await dialog.confirm(`Delete field "${fieldName}"?`, { message: "This may cause data loss." }))) return;
                        updateTable((t) => {
                          const { [fieldName]: _, ...rest } = t.fields;
                          return { ...t, fields: rest };
                        });
                      }}
                    />
                  ))}
                </tbody>
              </table>
            </div>
            <button
              onClick={() => {
                const fieldName = `new_field_${fieldEntries.length + 1}`;
                updateTable((t) => ({
                  ...t,
                  fields: {
                    ...t.fields,
                    [fieldName]: { type: "text" },
                  },
                }));
              }}
              className="mt-3 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-dashed border-border text-sm text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
            >
              <Plus size={14} />
              Add Field
            </button>
          </Tabs.Content>

          {/* Indexes Tab */}
          <Tabs.Content value="indexes">
            <div className="space-y-3">
              {(table.indexes || []).map((idx, i) => (
                <div
                  key={i}
                  className="flex items-start gap-3 p-4 rounded-lg border border-border bg-primary"
                >
                  <div className="flex-1 space-y-3">
                    <div>
                      <label className="block text-xs font-medium text-muted-foreground mb-1">Columns</label>
                      <TagInput
                        value={idx.columns}
                        onChange={(cols) =>
                          updateTable((t) => {
                            const indexes = [...(t.indexes || [])];
                            indexes[i] = { ...indexes[i]!, columns: cols };
                            return { ...t, indexes };
                          })
                        }
                        suggestions={allTableColumns}
                        placeholder="Select columns..."
                      />
                    </div>
                    <div className="flex gap-4">
                      <Toggle
                        checked={idx.unique}
                        onChange={(v) =>
                          updateTable((t) => {
                            const indexes = [...(t.indexes || [])];
                            indexes[i] = { ...indexes[i]!, unique: v };
                            return { ...t, indexes };
                          })
                        }
                        label="Unique"
                      />
                      <div className="flex-1">
                        <input
                          type="text"
                          value={idx.where || ""}
                          onChange={(e) =>
                            updateTable((t) => {
                              const indexes = [...(t.indexes || [])];
                              indexes[i] = { ...indexes[i]!, where: e.target.value };
                              return { ...t, indexes };
                            })
                          }
                          placeholder="WHERE clause (optional)"
                          className="w-full px-3 py-1.5 rounded-lg border border-border bg-input text-sm text-foreground font-mono placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
                        />
                      </div>
                    </div>
                  </div>
                  <button
                    onClick={() =>
                      updateTable((t) => ({
                        ...t,
                        indexes: (t.indexes || []).filter((_, j) => j !== i),
                      }))
                    }
                    className="p-1.5 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              ))}
              <button
                onClick={() =>
                  updateTable((t) => ({
                    ...t,
                    indexes: [
                      ...(t.indexes || []),
                      { columns: [], unique: false, where: "" },
                    ],
                  }))
                }
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-dashed border-border text-sm text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
              >
                <Plus size={14} />
                Add Index
              </button>
            </div>
          </Tabs.Content>

          {/* RLS Tab */}
          <Tabs.Content value="rls">
            <div className="space-y-3">
              {(table.rls || []).map((policy, i) => (
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
                                updateTable((tbl) => {
                                  const rls = [...(tbl.rls || [])];
                                  rls[i] = { ...rls[i]!, type: t };
                                  return { ...tbl, rls };
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
                      <div>
                        <label className="block text-xs font-medium text-muted-foreground mb-2">Operations</label>
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
                                  updateTable((t) => {
                                    const rls = [...(t.rls || [])];
                                    const ops = e.target.checked
                                      ? [...(rls[i]!.operations || []), op]
                                      : (rls[i]!.operations || []).filter((o) => o !== op);
                                    rls[i] = { ...rls[i]!, operations: ops };
                                    return { ...t, rls };
                                  })
                                }
                                className="rounded border-border"
                              />
                              {op}
                            </label>
                          ))}
                        </div>
                      </div>
                    </div>
                    <button
                      onClick={() =>
                        updateTable((t) => ({
                          ...t,
                          rls: (t.rls || []).filter((_, j) => j !== i),
                        }))
                      }
                      className="p-1.5 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-muted-foreground mb-1">Check Expression</label>
                    <CodeEditor
                      value={policy.check || ""}
                      onChange={(val) =>
                        updateTable((t) => {
                          const rls = [...(t.rls || [])];
                          rls[i] = { ...rls[i]!, check: val };
                          return { ...t, rls };
                        })
                      }
                      minHeight="60px"
                    />
                    <div className="flex gap-2 mt-2">
                      {[
                        { label: "Owner only", expr: "user_id = auth.uid()" },
                        { label: "Authenticated", expr: "auth.is_authenticated()" },
                      ].map(({ label, expr }) => (
                        <button
                          key={label}
                          onClick={() =>
                            updateTable((t) => {
                              const rls = [...(t.rls || [])];
                              rls[i] = { ...rls[i]!, check: expr };
                              return { ...t, rls };
                            })
                          }
                          className="px-2 py-1 rounded border border-border text-xs text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
                        >
                          {label}
                        </button>
                      ))}
                    </div>
                  </div>
                </div>
              ))}
              <button
                onClick={() =>
                  updateTable((t) => ({
                    ...t,
                    rls: [
                      ...(t.rls || []),
                      { operations: ["select"], check: "" },
                    ],
                  }))
                }
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-dashed border-border text-sm text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
              >
                <Plus size={14} />
                Add RLS Policy
              </button>
            </div>
          </Tabs.Content>

          {/* Search Tab */}
          <Tabs.Content value="search">
            <div className="space-y-4 max-w-lg">
              <div>
                <label className="block text-sm font-medium text-foreground mb-2">Search Config</label>
                <select
                  value={table.search_config || "english"}
                  onChange={(e) =>
                    updateTable((t) => ({ ...t, search_config: e.target.value }))
                  }
                  className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
                >
                  {SEARCH_CONFIGS.map((c) => (
                    <option key={c} value={c}>
                      {c}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-foreground mb-2">Searchable Columns</label>
                <div className="space-y-1.5">
                  {fieldEntries
                    .filter(([_, f]) => f.type === "text" || f.type?.startsWith("varchar"))
                    .map(([fieldName]) => (
                      <label
                        key={fieldName}
                        className="flex items-center gap-2 text-sm text-foreground cursor-pointer"
                      >
                        <input
                          type="checkbox"
                          checked={(table.searchable || []).includes(fieldName)}
                          onChange={(e) =>
                            updateTable((t) => ({
                              ...t,
                              searchable: e.target.checked
                                ? [...(t.searchable || []), fieldName]
                                : (t.searchable || []).filter((s) => s !== fieldName),
                            }))
                          }
                          className="rounded border-border"
                        />
                        <span className="font-mono">{fieldName}</span>
                      </label>
                    ))}
                </div>
              </div>
            </div>
          </Tabs.Content>

          {/* Seeds Tab */}
          <Tabs.Content value="seeds">
            <SeedsTab
              tableFields={fieldEntries}
              seeds={seeds}
              onChange={(rows) => {
                setSeeds(rows);
                setDirty(true);
              }}
            />
          </Tabs.Content>

        </Tabs.Root>

        {/* Preview Pane */}
        <div className="mt-8 border-t border-border pt-4">
          <button
            onClick={() => setShowPreview(!showPreview)}
            className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
          >
            {showPreview ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
            Migration Preview
          </button>
          {showPreview && diff && (
            <div className="mt-3">
              <DiffViewer
                statements={diff.statements}
                isDestructive={diff.is_destructive}
              />
            </div>
          )}
        </div>
      </div>

      <SaveBar
        onSave={handleSave}
        saving={saving}
        errors={saveErrors}
        dirty={dirty}
      />
    </div>
  );
}

// Seeds Tab Component
function SeedsTab({
  tableFields,
  seeds,
  onChange,
}: {
  tableFields: [string, Field][];
  seeds: Record<string, unknown>[];
  onChange: (rows: Record<string, unknown>[]) => void;
}) {
  const dialog = useDialog();

  return (
    <div>
      {tableFields.length === 0 ? (
        <p className="text-sm text-muted-foreground">Add fields to the table first.</p>
      ) : (
        <>
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
                      <span className="ml-1 text-muted-foreground/50">{field.type || (field.foreign_key ? "bigint" : "text")}</span>
                    </th>
                  ))}
                  <th className="w-10" />
                </tr>
              </thead>
              <tbody>
                {seeds.map((row, rowIdx) => (
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
                              const updated = [...seeds];
                              updated[rowIdx] = { ...updated[rowIdx]!, [fieldName]: v };
                              onChange(updated);
                            }}
                          />
                        ) : field.enum && field.enum.length > 0 ? (
                          <select
                            value={String(row[fieldName] ?? "")}
                            onChange={(e) => {
                              const updated = [...seeds];
                              updated[rowIdx] = { ...updated[rowIdx]!, [fieldName]: e.target.value };
                              onChange(updated);
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
                                : "text"
                            }
                            value={String(row[fieldName] ?? "")}
                            onChange={(e) => {
                              const updated = [...seeds];
                              updated[rowIdx] = { ...updated[rowIdx]!, [fieldName]: e.target.value };
                              onChange(updated);
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
                          onChange(seeds.filter((_, i) => i !== rowIdx));
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
          </div>

          <button
            onClick={() => {
              const newRow: Record<string, unknown> = {};
              tableFields.forEach(([fieldName]) => {
                newRow[fieldName] = "";
              });
              onChange([...seeds, newRow]);
            }}
            className="mt-3 inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-dashed border-border text-sm text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
          >
            <Plus size={14} />
            Add Row
          </button>
        </>
      )}
    </div>
  );
}

// Field Row Component
function FieldRow({
  name,
  field,
  fkOptions,
  onChange,
  onRename,
  onDelete,
}: {
  name: string;
  field: Field;
  fkOptions: string[];
  onChange: (f: Field) => void;
  onRename: (newName: string) => void;
  onDelete: () => void;
}) {
  const [editingName, setEditingName] = useState(false);
  const [localName, setLocalName] = useState(name);

  return (
    <tr className="border-b border-border/50 hover:bg-surface-hover/30">
      <td className="px-1 py-2 text-muted-foreground">
        <GripVertical size={12} className="cursor-grab" />
      </td>
      <td className="px-3 py-2">
        {editingName ? (
          <input
            autoFocus
            value={localName}
            onChange={(e) => setLocalName(e.target.value)}
            onBlur={() => {
              setEditingName(false);
              if (localName !== name && localName.trim()) onRename(localName.trim());
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                setEditingName(false);
                if (localName !== name && localName.trim()) onRename(localName.trim());
              }
            }}
            className="w-full px-2 py-0.5 rounded border border-ring bg-input text-sm font-mono text-foreground focus:outline-none"
          />
        ) : (
          <button
            onClick={() => setEditingName(true)}
            className="text-sm font-mono text-foreground hover:text-accent transition-colors cursor-pointer"
          >
            {name}
          </button>
        )}
      </td>
      <td className="px-3 py-2">
        <select
          value={field.type || (field.foreign_key ? "bigint" : "text")}
          onChange={(e) => onChange({ ...field, type: e.target.value })}
          className="w-full px-2 py-0.5 rounded border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
        >
          {POSTGRES_TYPES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </td>
      <td className="px-2 py-2">
        <div className="flex justify-center">
          <Toggle
            aria-label="primary key"
            checked={field.primary_key || false}
            onChange={(v) => onChange({ ...field, primary_key: v })}
          />
        </div>
      </td>
      <td className="px-2 py-2">
        <div className="flex justify-center">
          <Toggle
            aria-label="required"
            checked={field.required || false}
            onChange={(v) => onChange({ ...field, required: v })}
          />
        </div>
      </td>
      <td className="px-2 py-2">
        <div className="flex justify-center">
          <Toggle
            aria-label="unique"
            checked={field.unique || false}
            onChange={(v) => onChange({ ...field, unique: v })}
          />
        </div>
      </td>
      <td className="px-3 py-2">
        <input
          type="text"
          value={String(field.default ?? "")}
          onChange={(e) =>
            onChange({ ...field, default: e.target.value || undefined })
          }
          placeholder="—"
          list="sql-defaults"
          className="w-full px-2 py-0.5 rounded border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
        />
        <datalist id="sql-defaults">
          {SQL_DEFAULTS.map((d) => (
            <option key={d} value={d} />
          ))}
        </datalist>
      </td>
      <td className="px-3 py-2">
        <select
          value={field.foreign_key?.references || ""}
          onChange={(e) =>
            onChange({
              ...field,
              foreign_key: e.target.value
                ? { references: e.target.value, on_delete: "cascade" }
                : undefined,
            })
          }
          className="w-full px-2 py-0.5 rounded border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
        >
          <option value="">—</option>
          {fkOptions.map((fk) => (
            <option key={fk} value={fk}>
              {fk}
            </option>
          ))}
        </select>
      </td>
      <td className="px-1 py-2">
        <button
          onClick={onDelete}
          className="p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
          aria-label={`Delete field ${name}`}
        >
          <Trash2 size={13} />
        </button>
      </td>
    </tr>
  );
}
