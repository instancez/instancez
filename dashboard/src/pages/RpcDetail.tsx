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
import { Toggle } from "../components/Toggle";
import { POSTGRES_TYPES } from "../lib/utils";
import type { RpcFunction, FuncArg } from "../lib/types";

const LANGUAGES = ["plpgsql", "sql"];
const VOLATILITIES = ["volatile", "stable", "immutable"];
const SECURITIES = ["invoker", "definer"];

export function RpcDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [fn, setFn] = useState<RpcFunction | null>(null);
  const [dirty, setDirty] = useState(false);
  const [showTest, setShowTest] = useState(false);
  const [testInputs, setTestInputs] = useState<Record<string, string>>({});
  const [testResult, setTestResult] = useState<string | null>(null);
  const [testLoading, setTestLoading] = useState(false);

  useEffect(() => {
    if (config && name && (config.rpc || {})[name]) {
      setFn(structuredClone((config.rpc || {})[name]!));
      setDirty(false);
    }
  }, [config, name]);

  function updateFn(updater: (prev: RpcFunction) => RpcFunction) {
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
      rpc: { ...(config.rpc || {}), [name]: fn },
    };
    await save(updated);
    setDirty(false);
  }

  async function deleteFunction() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete function "${name}"?`, { message: "This will permanently remove the function endpoint.", confirmText: name }))) return;
    const { [name]: _, ...rest } = config.rpc || {};
    const updated = { ...config, rpc: rest };
    const ok = await save(updated);
    if (ok) navigate("/rpc");
  }

  async function runTest() {
    if (!name || !fn) return;
    setTestLoading(true);
    setTestResult(null);
    try {
      const key = sessionStorage.getItem("ultrabase_admin_key") || "";
      const args: Record<string, unknown> = {};
      for (const a of fn.args || []) {
        if (testInputs[a.name] !== undefined && testInputs[a.name] !== "") {
          args[a.name] = testInputs[a.name];
        }
      }
      const res = await fetch(`/rest/v1/rpc/${name}`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Admin-Key": key,
        },
        body: JSON.stringify(args),
      });
      const text = await res.text();
      try {
        setTestResult(JSON.stringify(JSON.parse(text), null, 2));
      } catch {
        setTestResult(text);
      }
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

  const args = fn.args || [];

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description={fn.description || "RPC function"}
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate("/rpc")}
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
          <div className="flex items-end pb-2">
            <Toggle
              checked={fn.auth_required}
              onChange={(v) => updateFn((f) => ({ ...f, auth_required: v }))}
              label="Auth required"
            />
          </div>
        </div>

        {/* Function Properties */}
        <div className="grid grid-cols-3 gap-4">
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Language</label>
            <select
              value={fn.language || "plpgsql"}
              onChange={(e) => updateFn((f) => ({ ...f, language: e.target.value }))}
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
            >
              {LANGUAGES.map((l) => (
                <option key={l} value={l}>{l}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Volatility</label>
            <select
              value={fn.volatility || "volatile"}
              onChange={(e) => updateFn((f) => ({ ...f, volatility: e.target.value }))}
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
            >
              {VOLATILITIES.map((v) => (
                <option key={v} value={v}>{v}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Security</label>
            <select
              value={fn.security || "invoker"}
              onChange={(e) => updateFn((f) => ({ ...f, security: e.target.value }))}
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors cursor-pointer"
            >
              {SECURITIES.map((s) => (
                <option key={s} value={s}>{s}</option>
              ))}
            </select>
          </div>
        </div>

        {/* Return Type */}
        <div>
          <label className="block text-xs font-medium text-muted-foreground mb-1">Return Type</label>
          <input
            type="text"
            value={fn.returns?.type || ""}
            onChange={(e) =>
              updateFn((f) => ({
                ...f,
                returns: { ...f.returns, type: e.target.value },
              }))
            }
            placeholder="void, int, setof posts, etc."
            className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
          />
        </div>

        {/* Function Body */}
        <div>
          <label className="block text-sm font-medium text-foreground mb-2">Function Body</label>
          <CodeEditor
            value={fn.body || ""}
            onChange={(val) => updateFn((f) => ({ ...f, body: val }))}
            language="sql"
            minHeight="160px"
          />
        </div>

        {/* Arguments */}
        <div>
          <div className="flex items-center justify-between mb-3">
            <label className="text-sm font-medium text-foreground">Arguments</label>
            <button
              onClick={async () => {
                const argName = await dialog.prompt("Argument name:");
                if (!argName?.trim()) return;
                updateFn((f) => ({
                  ...f,
                  args: [...(f.args || []), { name: argName.trim(), type: "text", required: false }],
                }));
              }}
              className="inline-flex items-center gap-1 px-2 py-1 rounded border border-dashed border-border text-xs text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
            >
              <Plus size={12} />
              Add Arg
            </button>
          </div>
          {args.length > 0 ? (
            <div className="space-y-2">
              {args.map((arg, idx) => (
                <div
                  key={arg.name}
                  className="flex items-center gap-3 px-3 py-2 rounded-lg border border-border bg-primary"
                >
                  <span className="text-sm font-mono text-foreground min-w-[100px]">{arg.name}</span>
                  <select
                    value={arg.type}
                    onChange={(e) =>
                      updateFn((f) => {
                        const newArgs = [...(f.args || [])];
                        const cur = newArgs[idx];
                        if (!cur) return f;
                        newArgs[idx] = { ...cur, type: e.target.value };
                        return { ...f, args: newArgs };
                      })
                    }
                    className="px-2 py-1 rounded border border-border bg-input text-xs font-mono text-foreground cursor-pointer focus:outline-none focus:border-ring"
                  >
                    {POSTGRES_TYPES.map((t) => (
                      <option key={t} value={t}>{t}</option>
                    ))}
                  </select>
                  <Toggle
                    checked={arg.required}
                    onChange={(v) =>
                      updateFn((f) => {
                        const newArgs = [...(f.args || [])];
                        const cur = newArgs[idx];
                        if (!cur) return f;
                        newArgs[idx] = { ...cur, required: v };
                        return { ...f, args: newArgs };
                      })
                    }
                    label="Required"
                  />
                  <button
                    onClick={() =>
                      updateFn((f) => ({
                        ...f,
                        args: (f.args || []).filter((_, i) => i !== idx),
                      }))
                    }
                    className="ml-auto p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No arguments defined.</p>
          )}
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
              {args.map((arg) => (
                <div key={arg.name}>
                  <label className="block text-xs font-medium text-muted-foreground mb-1">{arg.name}</label>
                  <input
                    type="text"
                    value={testInputs[arg.name] || ""}
                    onChange={(e) =>
                      setTestInputs((prev) => ({ ...prev, [arg.name]: e.target.value }))
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
