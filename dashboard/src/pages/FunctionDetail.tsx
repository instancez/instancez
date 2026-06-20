import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect, useCallback } from "react";
import { Trash2, Plus, Settings2, KeyRound, Code2 } from "lucide-react";
import { Box, Grid, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { DetailToolbar } from "../components/DetailToolbar";
import { SaveBar } from "../components/SaveBar";
import { Toggle } from "../components/Toggle";
import { CodeEditor } from "../components/CodeEditor";
import { Button, Field, Input, Panel, Section, Select } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import type { CodeFunction } from "../lib/types";

// Code-function runtimes instancez supports. validateCodeFunctions rejects
// anything else, so this is a closed set rendered as a dropdown.
const RUNTIMES = ["node"];

export function FunctionDetail() {
  const backend = useBackend();
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const canWriteConfig = backend.capabilities.canWriteConfig;
  const dialog = useDialog();
  const [fn, setFn] = useState<CodeFunction | null>(null);
  const [code, setCode] = useState<string | null>(null);
  const [codeDirty, setCodeDirty] = useState(false);
  const [codeSaving, setCodeSaving] = useState(false);
  const [codeError, setCodeError] = useState<string | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);

  useEffect(() => {
    if (config && name && (config.functions || {})[name]) {
      setFn(structuredClone((config.functions || {})[name]!));
    }
  }, [config, name]);

  useEffect(() => {
    if (!name) return;
    backend.getFunctionCode(name)
      .then((r) => setCode(r.content))
      .catch(() => setCode(null)); // not available (e.g. readonly mode / no configPath)
  }, [backend, name]);

  const handleCodeSave = useCallback(async () => {
    if (!name || code === null) return;
    setCodeSaving(true);
    setCodeError(null);
    try {
      await backend.putFunctionCode(name, code);
      setCodeDirty(false);
    } catch (e: any) {
      setCodeError(e.message || "Failed to save");
    } finally {
      setCodeSaving(false);
    }
  }, [backend, name, code]);

  function updateFn(updater: (prev: CodeFunction) => CodeFunction) {
    setFn((prev) => {
      if (!prev) return prev;
      return updater(prev);
    });
  }

  async function handleSave() {
    if (!config || !fn || !name) return;
    setFileError(null);

    // A changed file path must exist on disk before the save can conclude.
    const savedFile = (config.functions || {})[name]?.file ?? "";
    if (backend.capabilities.canEditFunctionCode && fn.file && fn.file !== savedFile) {
      try {
        const { exists } = await backend.checkFunctionFile(fn.file);
        if (!exists) {
          setFileError(`File not found: ${fn.file} — create it first or fix the path.`);
          return;
        }
      } catch {
        setFileError(`Could not verify that ${fn.file} exists; save aborted.`);
        return;
      }
    }

    const updated = {
      ...config,
      functions: { ...(config.functions || {}), [name]: fn },
    };
    await save(updated);
  }

  async function deleteFunction() {
    if (!config || !name) return;
    if (
      !(await dialog.confirm(`Delete function "${name}"?`, {
        message: "Removes the config entry. The .js file is left on disk.",
        confirmText: name,
      }))
    )
      return;
    const { [name]: _omit, ...rest } = config.functions || {};
    const ok = await save({ ...config, functions: rest });
    if (ok) navigate("..", { relative: "path" });
  }

  if (!config || !fn || !name) {
    return (
      <Box p="8">
        <Text fontSize="sm" color="fg.muted">Function not found.</Text>
      </Box>
    );
  }

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty = !jsonEqual(fn, (config.functions || {})[name] ?? null);

  const envEntries = Object.entries(fn.env || {});

  return (
    <Box pb="20">
      <DetailToolbar backLabel="Code Functions" onDelete={deleteFunction} />
      <VStack pb="8" gap="6" maxW="3xl" align="stretch">
        <Section
          title="Runtime"
          icon={Settings2}
        >
          <Grid gridTemplateColumns="repeat(2, 1fr)" gap="4">
            <Field label="Runtime">
              <Select
                value={fn.runtime || "node"}
                onChange={(e) => updateFn((f) => ({ ...f, runtime: e.target.value }))}
              >
                {RUNTIMES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label="File"
              hint={
                backend.capabilities.canEditFunctionFile
                  ? undefined
                  : "Managed by instancez — the function's file path can't be changed here."
              }
            >
              <Input
                mono
                value={fn.file || ""}
                onChange={(e) => updateFn((f) => ({ ...f, file: e.target.value }))}
                placeholder="functions/name.js"
                readOnly={!backend.capabilities.canEditFunctionFile}
              />
            </Field>
            <Field label="Timeout">
              <Input
                mono
                value={fn.timeout || ""}
                onChange={(e) => updateFn((f) => ({ ...f, timeout: e.target.value }))}
                placeholder="30s"
              />
            </Field>
            <HStack align="end" pb="2">
              <Toggle
                checked={fn.auth_required}
                onChange={(v) => updateFn((f) => ({ ...f, auth_required: v }))}
                label="Auth required"
              />
            </HStack>
          </Grid>
        </Section>

        <Section
          title="Environment"
          icon={KeyRound}
          actions={
            <Button
              variant="dashed"
              size="sm"
              onClick={async () => {
                const key = await dialog.prompt("Env variable name:");
                const trimmed = key?.trim();
                if (!trimmed) return;
                if (fn.env && trimmed in fn.env) {
                  await dialog.alert(`Variable "${trimmed}" already exists`, {
                    message: "Edit its value in the list instead of adding it again.",
                  });
                  return;
                }
                updateFn((f) => ({ ...f, env: { ...(f.env || {}), [trimmed]: "" } }));
              }}
            >
              <Plus size={14} />
              Add Var
            </Button>
          }
        >
          {envEntries.length > 0 ? (
            <VStack gap="2" align="stretch">
              {envEntries.map(([key, val]) => (
                <Panel key={key} display="flex" alignItems="center" gap="3" px="3" py="2">
                  <Text fontSize="sm" fontFamily="mono" color="fg" minW="140px">{key}</Text>
                  <Input
                    mono
                    inputSize="sm"
                    value={val}
                    onChange={(e) =>
                      updateFn((f) => ({ ...f, env: { ...(f.env || {}), [key]: e.target.value } }))
                    }
                    style={{ flex: 1 }}
                  />
                  <Button
                    variant="danger-ghost"
                    size="icon"
                    aria-label={`Delete ${key}`}
                    onClick={() =>
                      updateFn((f) => {
                        const next = { ...(f.env || {}) };
                        delete next[key];
                        return { ...f, env: next };
                      })
                    }
                  >
                    <Trash2 size={13} />
                  </Button>
                </Panel>
              ))}
            </VStack>
          ) : (
            <Text fontSize="sm" color="fg.muted">No environment variables.</Text>
          )}
        </Section>
        {backend.capabilities.canEditFunctionCode && code !== null && (
          <Section
            title="Code"
            icon={Code2}
            actions={
              codeDirty ? (
                <Button
                  size="sm"
                  onClick={handleCodeSave}
                  disabled={codeSaving}
                >
                  {codeSaving ? "Saving…" : "Save code"}
                </Button>
              ) : null
            }
          >
            {codeError && (
              <Text fontSize="sm" color="fg.error" mb="2">{codeError}</Text>
            )}
            <Box borderRadius="md" borderWidth="1px" borderColor="border" overflow="hidden">
              <CodeEditor
                value={code}
                onChange={(v) => { setCode(v); setCodeDirty(true); }}
                language="javascript"
                minHeight="320px"
              />
            </Box>
          </Section>
        )}
      </VStack>

      {canWriteConfig && (
        <SaveBar
          onSave={handleSave}
          saving={saving}
          errors={
            fileError
              ? [...saveErrors, { path: `functions.${name}.file`, message: fileError }]
              : saveErrors
          }
          dirty={dirty}
        />
      )}
    </Box>
  );
}
