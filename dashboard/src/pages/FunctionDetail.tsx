import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { ArrowLeft, Trash2, Plus } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { Toggle } from "../components/Toggle";
import type { CodeFunction } from "../lib/types";

// Code-function runtimes instancez supports. validateCodeFunctions rejects
// anything else, so this is a closed set rendered as a dropdown.
const RUNTIMES = ["node"];

export function FunctionDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [fn, setFn] = useState<CodeFunction | null>(null);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (config && name && (config.functions || {})[name]) {
      setFn(structuredClone((config.functions || {})[name]!));
      setDirty(false);
    }
  }, [config, name]);

  function updateFn(updater: (prev: CodeFunction) => CodeFunction) {
    setFn((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  async function handleSave() {
    if (!config || !fn || !name) return;
    const updated = {
      ...config,
      functions: { ...(config.functions || {}), [name]: fn },
    };
    await save(updated);
    setDirty(false);
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

  const envEntries = Object.entries(fn.env || {});

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description="Code function served at /functions/v1/"
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate("/functions")}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
            >
              <ArrowLeft size={14} />
              Back
            </button>
            <button
              onClick={deleteFunction}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-destructive/30 text-sm text-destructive hover:bg-destructive/10 transition-colors cursor-pointer"
            >
              <Trash2 size={14} />
              Delete
            </button>
          </div>
        }
      />

      <div className="px-8 space-y-6 max-w-3xl">
        <p className="text-sm text-muted-foreground">
          Edit the handler source in{" "}
          <span className="font-mono text-foreground">{fn.file}</span>.
        </p>

        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Runtime</label>
            <select
              value={fn.runtime || "node"}
              onChange={(e) => updateFn((f) => ({ ...f, runtime: e.target.value }))}
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
            >
              {RUNTIMES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">File</label>
            <input
              type="text"
              value={fn.file || ""}
              onChange={(e) => updateFn((f) => ({ ...f, file: e.target.value }))}
              placeholder="functions/name.js"
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Timeout</label>
            <input
              type="text"
              value={fn.timeout || ""}
              onChange={(e) => updateFn((f) => ({ ...f, timeout: e.target.value }))}
              placeholder="30s"
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
            />
          </div>
          <div className="flex items-end pb-2">
            <Toggle
              checked={fn.auth_required}
              onChange={(v) => updateFn((f) => ({ ...f, auth_required: v }))}
              label="Auth required"
            />
          </div>
        </div>

        <div>
          <div className="flex items-center justify-between mb-3">
            <label className="text-sm font-medium text-foreground">Environment</label>
            <button
              onClick={async () => {
                const key = await dialog.prompt("Env variable name:");
                if (!key?.trim()) return;
                updateFn((f) => ({ ...f, env: { ...(f.env || {}), [key.trim()]: "" } }));
              }}
              className="inline-flex items-center gap-1 px-2 py-1 rounded border border-dashed border-border text-xs text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
            >
              <Plus size={12} />
              Add Var
            </button>
          </div>
          {envEntries.length > 0 ? (
            <div className="space-y-2">
              {envEntries.map(([key, val]) => (
                <div
                  key={key}
                  className="flex items-center gap-3 px-3 py-2 rounded-lg border border-border bg-primary"
                >
                  <span className="text-sm font-mono text-foreground min-w-[140px]">{key}</span>
                  <input
                    type="text"
                    value={val}
                    onChange={(e) =>
                      updateFn((f) => ({ ...f, env: { ...(f.env || {}), [key]: e.target.value } }))
                    }
                    className="flex-1 px-2 py-1 rounded border border-border bg-input text-xs font-mono text-foreground focus:outline-none focus:border-ring"
                  />
                  <button
                    onClick={() =>
                      updateFn((f) => {
                        const next = { ...(f.env || {}) };
                        delete next[key];
                        return { ...f, env: next };
                      })
                    }
                    className="p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No environment variables.</p>
          )}
        </div>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
