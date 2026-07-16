import { useNavigate } from "react-router-dom";
import { Plus, Database } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow } from "../components/ui";
import { useBackend } from "../console/BackendContext";

export function Rpc() {
  const backend = useBackend();
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();
  const canWriteConfig = backend.capabilities.canWriteConfig;

  if (!config) return null;

  const functions = Object.entries(config.rpc || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  async function addFunction() {
    const name = await dialog.prompt("Function name:");
    if (!name?.trim()) return;
    const fnName = name.trim().toLowerCase().replace(/\s+/g, "_");

    const updated = {
      ...config!,
      rpc: {
        ...(config!.rpc || {}),
        [fnName]: {
          description: "",
          auth_required: true,
          language: "plpgsql",
          volatility: "volatile",
          security: "invoker",
          args: [],
          body: "BEGIN\n  -- function body\nEND;",
          returns: { type: "void" },
        },
      },
    };

    const ok = await save(updated);
    if (ok) navigate(fnName, { relative: "path" });
  }

  const addButton = canWriteConfig ? (
    <Button onClick={addFunction}>
      <Plus size={14} />
      Add Function
    </Button>
  ) : null;

  return (
    <Box>
      <Box pb="8">
        <HStack justify="space-between" gap="4" pb="6">
          <Text fontSize="sm" color="fg.muted">
            {functions.length} SQL function{functions.length !== 1 ? "s" : ""}
          </Text>
          {addButton}
        </HStack>
        {functions.length === 0 ? (
          <EmptyState
            icon={Database}
            title="No SQL functions yet"
            description="Create Postgres functions exposed at /rest/v1/rpc."
            action={addButton}
          />
        ) : (
          <VStack gap="2" align="stretch">
            {functions.map(([name, fn]) => (
              <ListRow
                key={name}
                icon={Database}
                title={name}
                meta={fn.description || undefined}
                onClick={() => navigate(name, { relative: "path" })}
                badges={
                  <>
                    <StatusBadge variant="info">{fn.language}</StatusBadge>
                    {fn.auth_required && (
                      <StatusBadge variant="info">auth</StatusBadge>
                    )}
                    <StatusBadge variant="muted">
                      {(fn.args || []).length} arg
                      {(fn.args || []).length !== 1 ? "s" : ""}
                    </StatusBadge>
                  </>
                }
              />
            ))}
          </VStack>
        )}
      </Box>
    </Box>
  );
}
