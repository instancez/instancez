import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect, useCallback } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { Plus, Trash2, GripVertical } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { TagInput } from "../components/TagInput";
import { Toggle } from "../components/Toggle";
import { DiffViewer } from "../components/DiffViewer";
import { RlsPolicyCard } from "../components/RlsPolicyCard";
import {
  Button,
  Disclosure,
  Field as UiField,
  Input,
  Panel,
  Select,
} from "../components/ui";
import { getConfigDiff } from "../api/client";
import { POSTGRES_TYPES, SQL_DEFAULTS } from "../lib/utils";
import type { Table, Field, DiffResponse } from "../lib/types";

const RLS_QUICK_FILLS = [
  { label: "Owner only", expr: "user_id = auth.uid()" },
  { label: "Authenticated", expr: "auth.is_authenticated()" },
];

export function TableDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [table, setTable] = useState<Table | null>(null);
  const [seeds, setSeeds] = useState<Record<string, unknown>[]>([]);
  const [diff, setDiff] = useState<DiffResponse | null>(null);

  // true when the data entry for this table uses CSV-file references (read-only in the UI)
  const tableData = config?.data?.[name ?? ""];
  const isCSVData = tableData !== undefined && !Array.isArray(tableData);

  useEffect(() => {
    if (config && name && config.tables[name]) {
      setTable(structuredClone(config.tables[name]!));
      const entry = config.data?.[name];
      setSeeds(structuredClone(Array.isArray(entry) ? entry : []));
    }
  }, [config, name]);

  const updateTable = useCallback(
    (updater: (prev: Table) => Table) => {
      setTable((prev) => (prev ? updater(prev) : prev));
    },
    []
  );

  async function handleSave() {
    if (!config || !table || !name) return;
    const updatedData = { ...config.data };
    if (!isCSVData) {
      if (seeds.length > 0) {
        updatedData[name] = seeds;
      } else {
        delete updatedData[name];
      }
    }
    const updated = {
      ...config,
      tables: { ...config.tables, [name]: table },
      data: updatedData,
    };
    await save(updated);
  }

  async function loadDiff() {
    try {
      const d = await getConfigDiff();
      setDiff(d);
    } catch {
      // ignore
    }
  }

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

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const savedSeedsEntry = config.data?.[name];
  const dirty =
    !jsonEqual(table, config.tables[name] ?? null) ||
    (!isCSVData && !jsonEqual(seeds, Array.isArray(savedSeedsEntry) ? savedSeedsEntry : []));

  const fieldEntries = table.fields || [];
  const allTableColumns = fieldEntries.map((x) => x.name);

  // FK options: all table.column pairs
  const fkOptions: string[] = [];
  for (const [tName, t] of Object.entries(config.tables)) {
    for (const f of (t.fields || [])) {
      fkOptions.push(`${tName}.${f.name}`);
    }
  }
  fkOptions.push("users.id");

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description={`${fieldEntries.length} fields, ${(table.indexes || []).length} indexes, ${(table.rls || []).length} RLS policies`}
        backTo="/tables"
        onDelete={deleteTable}
      />

      <div className="px-8 pb-8">
        <Tabs.Root defaultValue="fields">
          <Tabs.List className="flex gap-1 border-b border-border mb-6">
            {["Fields", "Indexes", "RLS", "Seeds"].map((tab) => (
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
                  {fieldEntries.map((field, fieldIdx) => (
                    <FieldRow
                      key={field.name}
                      name={field.name}
                      field={field}
                      fkOptions={fkOptions}
                      onChange={(updated) =>
                        updateTable((t) => ({
                          ...t,
                          fields: t.fields.map((f, i) => i === fieldIdx ? updated : f),
                        }))
                      }
                      onRename={(newName) => {
                        updateTable((t) => ({
                          ...t,
                          fields: t.fields.map((f, i) => i === fieldIdx ? { ...f, name: newName } : f),
                        }));
                      }}
                      onDelete={async () => {
                        if (!(await dialog.confirm(`Delete field "${field.name}"?`, { message: "This may cause data loss." }))) return;
                        updateTable((t) => ({
                          ...t,
                          fields: t.fields.filter((_, i) => i !== fieldIdx),
                        }));
                      }}
                    />
                  ))}
                </tbody>
              </table>
            </div>
            <Button
              variant="dashed"
              size="sm"
              className="mt-3"
              onClick={() => {
                const fieldName = `new_field_${fieldEntries.length + 1}`;
                updateTable((t) => ({
                  ...t,
                  fields: [...t.fields, { name: fieldName, type: "text" }],
                }));
              }}
            >
              <Plus size={14} />
              Add Field
            </Button>
          </Tabs.Content>

          {/* Indexes Tab */}
          <Tabs.Content value="indexes">
            <div className="space-y-3">
              {(table.indexes || []).map((idx, i) => (
                <Panel key={i} className="flex items-start gap-3 p-4">
                  <div className="flex-1 space-y-3">
                    <UiField label="Columns">
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
                    </UiField>
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
                        <Input
                          mono
                          value={idx.where || ""}
                          onChange={(e) =>
                            updateTable((t) => {
                              const indexes = [...(t.indexes || [])];
                              indexes[i] = { ...indexes[i]!, where: e.target.value };
                              return { ...t, indexes };
                            })
                          }
                          placeholder="WHERE clause (optional)"
                        />
                      </div>
                    </div>
                  </div>
                  <Button
                    variant="danger-ghost"
                    size="icon"
                    aria-label="Delete index"
                    onClick={() =>
                      updateTable((t) => ({
                        ...t,
                        indexes: (t.indexes || []).filter((_, j) => j !== i),
                      }))
                    }
                  >
                    <Trash2 size={14} />
                  </Button>
                </Panel>
              ))}
              <Button
                variant="dashed"
                size="sm"
                onClick={() =>
                  updateTable((t) => ({
                    ...t,
                    indexes: [
                      ...(t.indexes || []),
                      { columns: [], unique: false, where: "" },
                    ],
                  }))
                }
              >
                <Plus size={14} />
                Add Index
              </Button>
            </div>
          </Tabs.Content>

          {/* RLS Tab */}
          <Tabs.Content value="rls">
            <div className="space-y-3">
              {(table.rls || []).map((policy, i) => (
                <RlsPolicyCard
                  key={i}
                  policy={policy}
                  quickFills={RLS_QUICK_FILLS}
                  onChange={(p) =>
                    updateTable((t) => {
                      const rls = [...(t.rls || [])];
                      rls[i] = p;
                      return { ...t, rls };
                    })
                  }
                  onDelete={() =>
                    updateTable((t) => ({
                      ...t,
                      rls: (t.rls || []).filter((_, j) => j !== i),
                    }))
                  }
                />
              ))}
              <Button
                variant="dashed"
                size="sm"
                onClick={() =>
                  updateTable((t) => ({
                    ...t,
                    rls: [
                      ...(t.rls || []),
                      { operations: ["select"], check: "" },
                    ],
                  }))
                }
              >
                <Plus size={14} />
                Add RLS Policy
              </Button>
            </div>
          </Tabs.Content>

          {/* Seeds Tab */}
          <Tabs.Content value="seeds">
            {isCSVData ? (
              <p className="text-sm text-muted-foreground">
                Seed data for this table uses CSV files and cannot be edited here.
              </p>
            ) : (
              <SeedsTab
                tableFields={fieldEntries}
                seeds={seeds}
                onChange={(rows) => setSeeds(rows)}
              />
            )}
          </Tabs.Content>

        </Tabs.Root>

        {/* Preview Pane */}
        <div className="mt-8">
          <Disclosure label="Migration Preview">
            <MigrationPreview diff={diff} onOpen={loadDiff} />
          </Disclosure>
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

function MigrationPreview({
  diff,
  onOpen,
}: {
  diff: DiffResponse | null;
  onOpen: () => void;
}) {
  // Fetch the diff when the pane is first revealed.
  useEffect(() => {
    onOpen();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!diff) return null;
  return (
    <DiffViewer statements={diff.statements} isDestructive={diff.is_destructive} />
  );
}

// Seeds Tab Component
function SeedsTab({
  tableFields,
  seeds,
  onChange,
}: {
  tableFields: Field[];
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
                  {tableFields.map((field) => (
                    <th
                      key={field.name}
                      className="text-left px-3 py-2 text-xs font-medium text-muted-foreground"
                    >
                      <span className="font-mono">{field.name}</span>
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
                    {tableFields.map((field) => (
                      <td key={field.name} className="px-3 py-1.5">
                        {field.type === "boolean" ? (
                          <Toggle
                            aria-label={field.name}
                            checked={!!row[field.name]}
                            onChange={(v) => {
                              const updated = [...seeds];
                              updated[rowIdx] = { ...updated[rowIdx]!, [field.name]: v };
                              onChange(updated);
                            }}
                          />
                        ) : field.enum && field.enum.length > 0 ? (
                          <Select
                            mono
                            inputSize="sm"
                            value={String(row[field.name] ?? "")}
                            onChange={(e) => {
                              const updated = [...seeds];
                              updated[rowIdx] = { ...updated[rowIdx]!, [field.name]: e.target.value };
                              onChange(updated);
                            }}
                          >
                            <option value="">—</option>
                            {field.enum.map((v) => (
                              <option key={v} value={v}>{v}</option>
                            ))}
                          </Select>
                        ) : (
                          <Input
                            mono
                            inputSize="sm"
                            type={
                              field.type === "integer" || field.type === "bigint"
                                ? "number"
                                : "text"
                            }
                            value={String(row[field.name] ?? "")}
                            onChange={(e) => {
                              const updated = [...seeds];
                              updated[rowIdx] = { ...updated[rowIdx]!, [field.name]: e.target.value };
                              onChange(updated);
                            }}
                            placeholder="—"
                          />
                        )}
                      </td>
                    ))}
                    <td className="px-1 py-1.5">
                      <Button
                        variant="danger-ghost"
                        size="icon"
                        aria-label={`Delete row ${rowIdx + 1}`}
                        onClick={async () => {
                          if (!(await dialog.confirm(`Delete row ${rowIdx + 1}?`))) return;
                          onChange(seeds.filter((_, i) => i !== rowIdx));
                        }}
                      >
                        <Trash2 size={12} />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <Button
            variant="dashed"
            size="sm"
            className="mt-3"
            onClick={() => {
              const newRow: Record<string, unknown> = {};
              tableFields.forEach((field) => {
                newRow[field.name] = "";
              });
              onChange([...seeds, newRow]);
            }}
          >
            <Plus size={14} />
            Add Row
          </Button>
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
          <Input
            mono
            inputSize="sm"
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
        <Select
          mono
          inputSize="sm"
          value={field.type || (field.foreign_key ? "bigint" : "text")}
          onChange={(e) => onChange({ ...field, type: e.target.value })}
        >
          {POSTGRES_TYPES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </Select>
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
        <Input
          mono
          inputSize="sm"
          value={String(field.default ?? "")}
          onChange={(e) =>
            onChange({ ...field, default: e.target.value || undefined })
          }
          placeholder="—"
          list="sql-defaults"
        />
        <datalist id="sql-defaults">
          {SQL_DEFAULTS.map((d) => (
            <option key={d} value={d} />
          ))}
        </datalist>
      </td>
      <td className="px-3 py-2">
        <Select
          mono
          inputSize="sm"
          value={field.foreign_key?.references || ""}
          onChange={(e) =>
            onChange({
              ...field,
              foreign_key: e.target.value
                ? { references: e.target.value, on_delete: "cascade" }
                : undefined,
            })
          }
        >
          <option value="">—</option>
          {fkOptions.map((fk) => (
            <option key={fk} value={fk}>
              {fk}
            </option>
          ))}
        </Select>
      </td>
      <td className="px-1 py-2">
        <Button
          variant="danger-ghost"
          size="icon"
          aria-label={`Delete field ${name}`}
          onClick={onDelete}
        >
          <Trash2 size={13} />
        </Button>
      </td>
    </tr>
  );
}
