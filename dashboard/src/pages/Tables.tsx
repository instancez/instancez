import { useNavigate } from "react-router-dom";
import { Plus, Table2 } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow } from "../components/ui";
import { useBackend } from "../console/BackendContext";

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
    const name = await dialog.prompt("Table name:");
    if (!name?.trim()) return;
    const tableName = name.trim().toLowerCase().replace(/\s+/g, "_");
    if (config!.tables?.[tableName]) {
      await dialog.confirm(`Table "${tableName}" already exists.`, { confirmText: "OK" });
      return;
    }
    const seed = {
      fields: [
        { name: "id", type: "bigserial", primary_key: true },
        { name: "created_at", type: "timestamptz", default: "now()" },
      ],
      indexes: [],
      rls: [],
    };
    navigate("new", { relative: "path", state: { tableName, seed } });
  }

  const addButton = canWriteConfig ? (
    <Button onClick={addTable}>
      <Plus size={14} />
      Add Table
    </Button>
  ) : null;

  return (
    <Box>
      <Box pb="8">
        <HStack justify="space-between" gap="4" pb="6">
          <Text fontSize="sm" color="fg.muted">
            {tables.length} table{tables.length !== 1 ? "s" : ""} defined
          </Text>
          {addButton}
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
