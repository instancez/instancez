import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect, useCallback } from "react";
import { Trash2, Plus, Settings2, KeyRound, Code2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
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
    if (fn.file && fn.file !== savedFile) {
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
    if (ok) navigate("/functions");
  }

  if (!config || !fn || !name) {
    return (
      <div className="p-8">
        <p className="text-sm text-muted-foreground">Function not found.</p>
      </div>
    );
  }

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty = !jsonEqual(fn, (config.functions || {})[name] ?? null);

  const envEntries = Object.entries(fn.env || {});

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description="Code function served at /functions/v1/"
        backTo="/functions"
        onDelete={deleteFunction}
      />

      <div className="px-8 pb-8 space-y-6 max-w-3xl">
        <Section
          title="Runtime"
          icon={Settings2}
        >
          <div className="grid grid-cols-2 gap-4">
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
            <Field label="File">
              <Input
                mono
                value={fn.file || ""}
                onChange={(e) => updateFn((f) => ({ ...f, file: e.target.value }))}
                placeholder="functions/name.js"
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
            <div className="flex items-end pb-2">
              <Toggle
                checked={fn.auth_required}
                onChange={(v) => updateFn((f) => ({ ...f, auth_required: v }))}
                label="Auth required"
              />
            </div>
          </div>
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
            <div className="space-y-2">
              {envEntries.map(([key, val]) => (
                <Panel key={key} className="flex items-center gap-3 px-3 py-2">
                  <span className="text-sm font-mono text-foreground min-w-[140px]">{key}</span>
                  <Input
                    mono
                    inputSize="sm"
                    className="flex-1"
                    value={val}
                    onChange={(e) =>
                      updateFn((f) => ({ ...f, env: { ...(f.env || {}), [key]: e.target.value } }))
                    }
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
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No environment variables.</p>
          )}
        </Section>
        {code !== null && (
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
              <p className="text-sm text-destructive mb-2">{codeError}</p>
            )}
            <div className="rounded-md border border-border overflow-hidden">
              <CodeEditor
                value={code}
                onChange={(v) => { setCode(v); setCodeDirty(true); }}
                language="javascript"
                minHeight="320px"
              />
            </div>
          </Section>
        )}
      </div>

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
    </div>
  );
}
