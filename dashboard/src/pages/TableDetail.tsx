import { useParams, useNavigate, useLocation } from "react-router-dom";
import { useState, useEffect, useCallback } from "react";
import { Tabs, Box, HStack, Text, VStack } from "@chakra-ui/react";
import { Plus, Trash2, GripVertical } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { DetailToolbar } from "../components/DetailToolbar";
import { SaveBar } from "../components/SaveBar";
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
import { useBackend } from "../console/BackendContext";
import { POSTGRES_TYPES, SQL_DEFAULTS } from "../lib/utils";
import type { Table, Field, DiffResponse } from "../lib/types";

const RLS_QUICK_FILLS = [
  { label: "Owner only", expr: "user_id = auth.uid()" },
  { label: "Authenticated", expr: "auth.is_authenticated()" },
];

export function TableDetail() {
  const backend = useBackend();
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const canWriteConfig = backend.capabilities.canWriteConfig;
  const [table, setTable] = useState<Table | null>(null);
  const [diff, setDiff] = useState<DiffResponse | null>(null);

  const isNew = !name;
  const newState = location.state as { tableName?: string; seed?: Table } | null;

  useEffect(() => {
    if (isNew && newState?.seed) {
      setTable(structuredClone(newState.seed));
      return;
    }
    if (config && name && config.tables[name]) {
      setTable(structuredClone(config.tables[name]!));
    }
  }, [config, name, isNew, newState]);

  const updateTable = useCallback(
    (updater: (prev: Table) => Table) => {
      setTable((prev) => (prev ? updater(prev) : prev));
    },
    []
  );

  async function handleSave() {
    const effectiveName = isNew ? newState?.tableName : name;
    if (!config || !table || !effectiveName) return;
    const updated = {
      ...config,
      tables: { ...config.tables, [effectiveName]: table },
    };
    const ok = await save(updated);
    if (ok && isNew) navigate(`../${effectiveName}`, { relative: "path", replace: true });
  }

  async function loadDiff() {
    try {
      const d = await backend.getConfigDiff();
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
    if (ok) navigate("..", { relative: "path" });
  }

  if (!config || !table) {
    return (
      <Box p="8">
        <Text fontSize="sm" color="fg.muted">Table not found.</Text>
      </Box>
    );
  }

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  // In new mode the table is always dirty (nothing saved yet).
  const dirty =
    isNew ||
    !jsonEqual(table, config.tables[name ?? ""] ?? null);

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
    <Box pb="20">
      <DetailToolbar backLabel="Tables" onDelete={canWriteConfig && !isNew ? deleteTable : undefined} />
      <Box pb="8">
        <Tabs.Root defaultValue="fields" lazyMount unmountOnExit>
          <Tabs.List borderBottomWidth="1px" mb="6" gap="1">
            {["Fields", "Indexes", "RLS"].map((tab) => (
              <Tabs.Trigger
                key={tab}
                value={tab.toLowerCase()}
                fontSize="sm"
                fontWeight="medium"
                color="fg.muted"
                px="4"
                py="2"
                cursor="pointer"
                _selected={{ color: "accent", borderBottomWidth: "2px", borderColor: "accent" } as any}
                _hover={{ color: "fg" }}
                mb="-1px"
              >
                {tab}
              </Tabs.Trigger>
            ))}
          </Tabs.List>

          {/* Fields Tab */}
          <Tabs.Content value="fields">
            <Box overflowX="auto">
              <Box as="table" w="full" fontSize="sm">
                <Box as="thead">
                  <Box as="tr" borderBottomWidth="1px">
                    <Box as="th" w="8" />
                    <Box as="th" textAlign="left" px="3" py="2" fontSize="xs" fontWeight="medium" color="fg.muted">Name</Box>
                    <Box as="th" textAlign="left" px="3" py="2" fontSize="xs" fontWeight="medium" color="fg.muted">Type</Box>
                    <Box as="th" textAlign="center" px="2" py="2" fontSize="xs" fontWeight="medium" color="fg.muted">PK</Box>
                    <Box as="th" textAlign="center" px="2" py="2" fontSize="xs" fontWeight="medium" color="fg.muted">Required</Box>
                    <Box as="th" textAlign="center" px="2" py="2" fontSize="xs" fontWeight="medium" color="fg.muted">Unique</Box>
                    <Box as="th" textAlign="left" px="3" py="2" fontSize="xs" fontWeight="medium" color="fg.muted">Default</Box>
                    <Box as="th" textAlign="left" px="3" py="2" fontSize="xs" fontWeight="medium" color="fg.muted">FK</Box>
                    <Box as="th" w="10" />
                  </Box>
                </Box>
                <Box as="tbody">
                  {fieldEntries.map((field, fieldIdx) => (
                    <FieldRow
                      key={field.name}
                      name={field.name}
                      field={field}
                      fkOptions={fkOptions}
                      canWrite={canWriteConfig}
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
                </Box>
              </Box>
            </Box>
            {canWriteConfig && (
              <Button
                variant="dashed"
                size="sm"
                mt="3"
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
            )}
          </Tabs.Content>

          {/* Indexes Tab */}
          <Tabs.Content value="indexes">
            <VStack gap="3" align="stretch">
              {(table.indexes || []).map((idx, i) => (
                <Panel key={i} display="flex" alignItems="flex-start" gap="3" p="4">
                  <Box flex="1">
                    <VStack gap="3" align="stretch">
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
                      <HStack gap="4">
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
                        <Box flex="1">
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
                        </Box>
                      </HStack>
                    </VStack>
                  </Box>
                  {canWriteConfig && (
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
                  )}
                </Panel>
              ))}
              {canWriteConfig && (
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
              )}
            </VStack>
          </Tabs.Content>

          {/* RLS Tab */}
          <Tabs.Content value="rls">
            <VStack gap="3" align="stretch">
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
              {canWriteConfig && (
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
              )}
            </VStack>
          </Tabs.Content>

        </Tabs.Root>

        {/* Preview Pane */}
        <Box mt="8">
          <Disclosure label="Migration Preview">
            <MigrationPreview diff={diff} onOpen={loadDiff} />
          </Disclosure>
        </Box>
      </Box>

      {canWriteConfig && (
        <SaveBar
          onSave={handleSave}
          saving={saving}
          errors={saveErrors}
          dirty={dirty}
        />
      )}
    </Box>
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

// Field Row Component
function FieldRow({
  name,
  field,
  fkOptions,
  canWrite,
  onChange,
  onRename,
  onDelete,
}: {
  name: string;
  field: Field;
  fkOptions: string[];
  canWrite: boolean;
  onChange: (f: Field) => void;
  onRename: (newName: string) => void;
  onDelete: () => void;
}) {
  const [editingName, setEditingName] = useState(false);
  const [localName, setLocalName] = useState(name);

  return (
    <Box as="tr" borderBottomWidth="1px" _hover={{ bg: "bg.subtle" }}>
      <Box as="td" px="1" py="2" color="fg.muted">
        <GripVertical size={12} style={{ cursor: "grab" }} />
      </Box>
      <Box as="td" px="3" py="2">
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
          <Box
            as="button"
            onClick={() => setEditingName(true)}
            fontSize="sm"
            fontFamily="mono"
            color="fg"
            _hover={{ color: "accent" }}
            transition="colors"
            cursor="pointer"
            background="none"
            border="none"
            p="0"
          >
            {name}
          </Box>
        )}
      </Box>
      <Box as="td" px="3" py="2">
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
      </Box>
      <Box as="td" px="2" py="2">
        <HStack justify="center">
          <Toggle
            aria-label="primary key"
            checked={field.primary_key || false}
            onChange={(v) => onChange({ ...field, primary_key: v })}
          />
        </HStack>
      </Box>
      <Box as="td" px="2" py="2">
        <HStack justify="center">
          <Toggle
            aria-label="required"
            checked={field.required || false}
            onChange={(v) => onChange({ ...field, required: v })}
          />
        </HStack>
      </Box>
      <Box as="td" px="2" py="2">
        <HStack justify="center">
          <Toggle
            aria-label="unique"
            checked={field.unique || false}
            onChange={(v) => onChange({ ...field, unique: v })}
          />
        </HStack>
      </Box>
      <Box as="td" px="3" py="2">
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
      </Box>
      <Box as="td" px="3" py="2">
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
      </Box>
      <Box as="td" px="1" py="2">
        {canWrite && (
          <Button
            variant="danger-ghost"
            size="icon"
            aria-label={`Delete field ${name}`}
            onClick={onDelete}
          >
            <Trash2 size={13} />
          </Button>
        )}
      </Box>
    </Box>
  );
}
