import { useNavigate } from "react-router-dom";
import { Plus, Table2, Trash2 } from "lucide-react";
import { Box, HStack, IconButton, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import { NAME_HINT, nameError } from "../lib/utils";

export function Tables() {
  const backend = useBackend();
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();
  const canWriteConfig = backend.capabilities.canWriteConfig;

  if (!config) return null;

  const tables = Object.entries(config.tables || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  async function addTable() {
    const name = await dialog.prompt("Table name", { message: NAME_HINT, validate: (v) => nameError(v, Object.keys(config!.tables ?? {}), "table") });
    if (!name?.trim()) return;
    const tableName = name.trim().toLowerCase().replace(/\s+/g, "_");
    const seed = {
      fields: [
        { name: "id", type: "bigserial", primary_key: true },
        { name: "created_at", type: "timestamptz", default: "now()" },
      ],
      indexes: [],
      rls_enabled: true,
      rls: [],
    };
    navigate("new", { relative: "path", state: { tableName, seed } });
  }

  async function deleteTable(name: string) {
    if (!(await dialog.confirm("Delete table?", { message: `"${name}" and all its data will be dropped. This can't be undone.`, confirmText: name }))) return;
    const { [name]: _, ...rest } = config!.tables;
    await save({ ...config!, tables: rest });
  }

  const addButton = canWriteConfig ? (
    <Button onClick={addTable}>
      <Plus size={14} />
      Add table
    </Button>
  ) : null;

  return (
    <Box>
      <Box pb="8">
        <HStack justify="space-between" gap="4" pb="6">
          <Text fontSize="sm" color="fg.muted">
            {tables.length} table{tables.length !== 1 ? "s" : ""} defined
          </Text>
          {tables.length > 0 && addButton}
        </HStack>
        {tables.length === 0 ? (
          <EmptyState
            icon={Table2}
            title="No tables yet"
            description="Define your first table to get started."
            action={addButton}
          />
        ) : (
          <VStack gap="2" align="stretch">
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
                  action={canWriteConfig ? (
                    <IconButton aria-label={`Delete table ${name}`} size="sm" variant="ghost" onClick={() => deleteTable(name)}>
                      <Trash2 size={14} />
                    </IconButton>
                  ) : undefined}
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
          </VStack>
        )}
      </Box>
    </Box>
  );
}
