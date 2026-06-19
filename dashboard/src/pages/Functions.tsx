import { useNavigate } from "react-router-dom";
import { useState, useEffect, useCallback } from "react";
import { Code2, Package, Trash2, CheckCircle, AlertCircle } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, Field, Input, ListRow, Panel, Section } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import { ListSkeleton } from "../components/Skeletons";

interface DepsState {
  dependencies: Record<string, string>;
  has_lock: boolean;
  readonly: boolean;
}

export function Functions() {
  const backend = useBackend();
  const { config } = useConfig();
  const navigate = useNavigate();

  const [deps, setDeps] = useState<DepsState | null>(null);
  const [depsLoading, setDepsLoading] = useState(false);
  const [addPkg, setAddPkg] = useState("");
  const [installing, setInstalling] = useState(false);
  const [installError, setInstallError] = useState<string | null>(null);
  const [installSuccess, setInstallSuccess] = useState<string | null>(null);
  useEffect(() => {
    setDepsLoading(true);
    backend.getFunctionDeps()
      .then(setDeps)
      .catch(() => setDeps(null)) // 501 or auth error → feature unavailable
      .finally(() => setDepsLoading(false));
  }, [backend]);

  const runNpm = useCallback(async (add: string[], remove: string[]) => {
    setInstalling(true);
    setInstallError(null);
    setInstallSuccess(null);
    try {
      const updated = await backend.postFunctionDeps(add, remove);
      setDeps(updated);
      if (add.length > 0) {
        setInstallSuccess(`Installed ${add.join(", ")}`);
        setTimeout(() => setInstallSuccess(null), 4000);
      }
      if (remove.length > 0) {
        setInstallSuccess(`Removed ${remove.join(", ")}`);
        setTimeout(() => setInstallSuccess(null), 4000);
      }
    } catch (e: any) {
      const detail = e.body?.detail || e.message || "npm failed";
      setInstallError(detail);
    } finally {
      setInstalling(false);
    }
  }, [backend]);

  const handleAdd = useCallback(() => {
    const pkg = addPkg.trim();
    if (!pkg || installing) return;
    setAddPkg("");
    runNpm([pkg], []);
  }, [addPkg, installing, runNpm]);

  const handleRemove = useCallback(
    (pkg: string) => {
      if (installing) return;
      runNpm([], [pkg]);
    },
    [installing, runNpm]
  );

  if (!config) return null;

  const functions = Object.entries(config.functions || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  const depEntries = deps ? Object.entries(deps.dependencies).sort(([a], [b]) => a.localeCompare(b)) : [];
  const canEdit = backend.capabilities.canManageDeps && deps !== null && !deps.readonly;

  return (
    <Box>
      <VStack pb="8" gap="6" maxW="3xl" align="stretch">
        <Text fontSize="sm" color="fg.muted">
          {functions.length} code function{functions.length !== 1 ? "s" : ""}
        </Text>
        {functions.length === 0 ? (
          <EmptyState
            icon={Code2}
            title="No code functions"
            description="Declare a function in instancez.yaml with a runtime and a .js file under functions/ (served at /functions/v1/<name>)."
          />
        ) : (
          <VStack gap="2" align="stretch">
            {functions.map(([name, fn]) => (
              <ListRow
                key={name}
                icon={Code2}
                title={name}
                meta={<Text as="span" fontFamily="mono">{fn.file}</Text>}
                onClick={() => navigate(name, { relative: "path" })}
                badges={
                  <>
                    <StatusBadge variant="info">{fn.runtime || "node"}</StatusBadge>
                    {fn.auth_required && (
                      <StatusBadge variant="info">auth</StatusBadge>
                    )}
                    {fn.timeout && (
                      <StatusBadge variant="muted">{fn.timeout}</StatusBadge>
                    )}
                  </>
                }
              />
            ))}
          </VStack>
        )}

        {/* Dependencies panel — only shown when the endpoint is reachable */}
        {(depsLoading || deps !== null) && (
          <Section
            title="Dependencies"
            icon={Package}
            actions={
              !depsLoading && deps !== null ? (
                deps.has_lock ? (
                  <StatusBadge variant="success">lock file</StatusBadge>
                ) : depEntries.length > 0 ? (
                  <StatusBadge variant="warning">no lock file</StatusBadge>
                ) : null
              ) : null
            }
          >
            {depsLoading ? (
              <ListSkeleton rows={3} />
            ) : (
              <>
                {depEntries.length > 0 ? (
                  <VStack gap="1.5" align="stretch">
                    {depEntries.map(([pkg, ver]) => (
                      <Panel key={pkg} display="flex" alignItems="center" gap="3" px="3" py="2">
                        <Text fontSize="sm" fontFamily="mono" color="fg" flex="1" truncate>
                          {pkg}
                        </Text>
                        <Text fontSize="xs" fontFamily="mono" color="fg.muted" flexShrink="0">
                          {ver as string}
                        </Text>
                        {canEdit && (
                          <Button
                            variant="danger-ghost"
                            size="icon"
                            aria-label={`Remove ${pkg}`}
                            disabled={installing}
                            onClick={() => handleRemove(pkg)}
                          >
                            <Trash2 size={13} />
                          </Button>
                        )}
                      </Panel>
                    ))}
                  </VStack>
                ) : (
                  <Text fontSize="sm" color="fg.muted">
                    No dependencies installed.
                  </Text>
                )}

                {canEdit && (
                  <Field label="Add package">
                    <HStack gap="2">
                      <Input
                        mono
                        placeholder="e.g. axios or axios@latest"
                        value={addPkg}
                        onChange={(e) => setAddPkg(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") handleAdd();
                        }}
                        disabled={installing}
                        style={{ flex: 1 }}
                      />
                      <Button
                        onClick={handleAdd}
                        disabled={installing || !addPkg.trim()}
                      >
                        {installing ? "Installing…" : "Install"}
                      </Button>
                    </HStack>
                  </Field>
                )}

                {installSuccess && (
                  <HStack gap="2" fontSize="sm" color="green.600">
                    <Box as={CheckCircle} boxSize="3.5" />
                    <Text>{installSuccess}</Text>
                  </HStack>
                )}
                {installError && (
                  <VStack gap="1" align="stretch">
                    <HStack gap="2" fontSize="sm" color="fg.error">
                      <Box as={AlertCircle} boxSize="3.5" />
                      <Text>npm failed</Text>
                    </HStack>
                    <Box
                      as="pre"
                      fontSize="xs"
                      color="fg.muted"
                      bg="bg.panel"
                      borderRadius="md"
                      p="2"
                      overflowX="auto"
                      whiteSpace="pre-wrap"
                    >
                      {installError}
                    </Box>
                  </VStack>
                )}
              </>
            )}
          </Section>
        )}
      </VStack>
    </Box>
  );
}
