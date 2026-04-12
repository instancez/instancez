import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import {
  ArrowLeft,
  Trash2,
  Plus,
  Play,
  ChevronDown,
  ChevronUp,
  Loader2,
} from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { HTTP_METHODS, RETURN_TYPES, POSTGRES_TYPES } from "../lib/utils";
import type { FunctionDef, FuncParam } from "../lib/types";

export function FunctionDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [fn, setFn] = useState<FunctionDef | null>(null);
  const [dirty, setDirty] = useState(false);
  const [showTest, setShowTest] = useState(false);
  const [testInputs, setTestInputs] = useState<Record<string, string>>({});
  const [testResult, setTestResult] = useState<string | null>(null);
  const [testLoading, setTestLoading] = useState(false);

  useEffect(() => {
    if (config && name && config.functions[name]) {
      setFn(structuredClone(config.functions[name]!));
      setDirty(false);
    }
  }, [config, name]);

  function updateFn(updater: (prev: FunctionDef) => FunctionDef) {
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
      functions: { ...config.functions, [name]: fn },
    };
    await save(updated);
    setDirty(false);
  }

  async function deleteFunction() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete function "${name}"?`, { message: "This will permanently remove the function endpoint.", confirmText: name }))) return;
    const { [name]: _, ...rest } = config.functions;
    const updated = { ...config, functions: rest };
    const ok = await save(updated);
    if (ok) navigate("/functions");
  }

  async function runTest() {
    if (!name) return;
    setTestLoading(true);
    setTestResult(null);
    try {
      const key = sessionStorage.getItem("ultrabase_admin_key") || "";
      const res = await fetch(`/api/fn/${name}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer test`,
          "X-Admin-Key": key,
        },
        body: JSON.stringify(testInputs),
      });
      const data = await res.json();
      setTestResult(JSON.stringify(data, null, 2));
    } catch (err: any) {
      setTestResult(`Error: ${err.message}`);
    } finally {
      setTestLoading(false);
    }
  }

  if (!config || !fn || !name) {
    return (
      <div className="p-8">
        <p className="text-sm text-muted-foreground">Function not found.</p>
      </div>
    );
  }

  const paramEntries = Object.entries(fn.params || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description={fn.description || "Custom SQL function"}
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
        {/* Basic Info */}
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Description</label>
            <input
              type="text"
              value={fn.description}
              onChange={(e) => updateFn((f) => ({ ...f, description: e.target.value }))}
              placeholder="What does this function do?"
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Method</label>
              <select
                value={fn.method}
                onChange={(e) => updateFn((f) => ({ ...f, method: e.target.value }))}
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
              >
                {HTTP_METHODS.map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
              </select>
            </div>
            <div className="flex items-end">
              <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer pb-2">
                <input
                  type="checkbox"
                  checked={fn.auth_required}
                  onChange={(e) => updateFn((f) => ({ ...f, auth_required: e.target.checked }))}
                  className="rounded border-border"
                />
                Auth required
              </label>
            </div>
          </div>
        </div>

        {/* SQL Query */}
        <div>
          <label className="block text-sm font-medium text-foreground mb-2">SQL Query</label>
          <CodeEditor
            value={fn.query}
            onChange={(val) => updateFn((f) => ({ ...f, query: val }))}
            language="sql"
            minHeight="160px"
          />
        </div>

        {/* Params */}
        <div>
          <div className="flex items-center justify-between mb-3">
            <label className="text-sm font-medium text-foreground">Parameters</label>
            <button
              onClick={async () => {
                const pName = await dialog.prompt("Parameter name:");
                if (!pName?.trim()) return;
                updateFn((f) => ({
                  ...f,
                  params: {
                    ...f.params,
                    [pName.trim()]: { type: "text", required: false },
                  },
                }));
              }}
              className="inline-flex items-center gap-1 px-2 py-1 rounded border border-dashed border-border text-xs text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
            >
              <Plus size={12} />
              Add Param
            </button>
          </div>
          {paramEntries.length > 0 ? (
            <div className="space-y-2">
              {paramEntries.map(([pName, param], idx) => (
                <div
                  key={pName}
                  className="flex items-center gap-3 px-3 py-2 rounded-lg border border-border bg-primary"
                >
                  <span className="text-xs font-mono text-accent w-6">${idx + 1}</span>
                  <span className="text-sm font-mono text-foreground min-w-[100px]">{pName}</span>
                  <select
                    value={param.type}
                    onChange={(e) =>
                      updateFn((f) => ({
                        ...f,
                        params: { ...f.params, [pName]: { ...param, type: e.target.value } },
                      }))
                    }
                    className="px-2 py-1 rounded border border-border bg-input text-xs font-mono text-foreground cursor-pointer focus:outline-none focus:border-ring"
                  >
                    {POSTGRES_TYPES.map((t) => (
                      <option key={t} value={t}>{t}</option>
                    ))}
                  </select>
                  <label className="flex items-center gap-1 text-xs text-foreground cursor-pointer">
                    <input
                      type="checkbox"
                      checked={param.required}
                      onChange={(e) =>
                        updateFn((f) => ({
                          ...f,
                          params: { ...f.params, [pName]: { ...param, required: e.target.checked } },
                        }))
                      }
                      className="rounded border-border"
                    />
                    Required
                  </label>
                  <button
                    onClick={() =>
                      updateFn((f) => {
                        const { [pName]: _, ...rest } = f.params;
                        return { ...f, params: rest };
                      })
                    }
                    className="ml-auto p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No parameters defined.</p>
          )}
        </div>

        {/* Returns */}
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Return Type</label>
            <select
              value={fn.returns.type}
              onChange={(e) =>
                updateFn((f) => ({
                  ...f,
                  returns: { ...f.returns, type: e.target.value },
                }))
              }
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
            >
              {RETURN_TYPES.map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </select>
          </div>
        </div>

        {/* Test Pane */}
        <div className="border-t border-border pt-4">
          <button
            onClick={() => setShowTest(!showTest)}
            className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
          >
            {showTest ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
            Test Function
          </button>
          {showTest && (
            <div className="mt-3 space-y-3">
              {paramEntries.map(([pName]) => (
                <div key={pName}>
                  <label className="block text-xs font-medium text-muted-foreground mb-1">{pName}</label>
                  <input
                    type="text"
                    value={testInputs[pName] || ""}
                    onChange={(e) =>
                      setTestInputs((prev) => ({ ...prev, [pName]: e.target.value }))
                    }
                    className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
                  />
                </div>
              ))}
              <button
                onClick={runTest}
                disabled={testLoading}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors disabled:opacity-50 cursor-pointer"
              >
                {testLoading ? (
                  <Loader2 size={14} className="animate-spin" />
                ) : (
                  <Play size={14} />
                )}
                Run
              </button>
              {testResult && (
                <pre className="p-4 rounded-lg bg-primary border border-border text-xs font-mono text-foreground overflow-x-auto max-h-64 overflow-y-auto">
                  {testResult}
                </pre>
              )}
            </div>
          )}
        </div>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
