import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { Trash2, Plus, Play, FileCode2, Braces } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { DetailToolbar } from "../components/DetailToolbar";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { Toggle } from "../components/Toggle";
import {
  Button,
  Disclosure,
  Field,
  Input,
  Panel,
  Section,
  Select,
} from "../components/ui";
import { POSTGRES_TYPES } from "../lib/utils";
import type { RpcFunction } from "../lib/types";
import { useBackend } from "../console/BackendContext";

const LANGUAGES = ["plpgsql", "sql"];
const VOLATILITIES = ["volatile", "stable", "immutable"];
const SECURITIES = ["invoker", "definer"];

export function RpcDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const backend = useBackend();
  const [fn, setFn] = useState<RpcFunction | null>(null);
  const [testInputs, setTestInputs] = useState<Record<string, string>>({});
  const [testResult, setTestResult] = useState<string | null>(null);
  const [testLoading, setTestLoading] = useState(false);

  useEffect(() => {
    if (config && name && (config.rpc || {})[name]) {
      setFn(structuredClone((config.rpc || {})[name]!));
    }
  }, [config, name]);

  function updateFn(updater: (prev: RpcFunction) => RpcFunction) {
    setFn((prev) => {
      if (!prev) return prev;
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
  }

  async function deleteFunction() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete function "${name}"?`, { message: "This will permanently remove the function endpoint.", confirmText: name }))) return;
    const { [name]: _, ...rest } = config.rpc || {};
    const updated = { ...config, rpc: rest };
    const ok = await save(updated);
    if (ok) navigate("..", { relative: "path" });
  }

  async function runTest() {
    if (!name || !fn) return;
    setTestLoading(true);
    setTestResult(null);
    try {
      const key = sessionStorage.getItem("instancez_admin_key") || "";
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

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty = !jsonEqual(fn, (config.rpc || {})[name] ?? null);

  const args = fn.args || [];

  return (
    <div className="pb-20">
      <DetailToolbar backLabel="Database Functions" onDelete={deleteFunction} />
      <div className="pb-8 space-y-6 max-w-3xl">
        <Section
          title="Definition"
          icon={FileCode2}
        >
          <div className="grid grid-cols-2 gap-4">
            <Field label="Description">
              <Input
                value={fn.description}
                onChange={(e) => updateFn((f) => ({ ...f, description: e.target.value }))}
                placeholder="What does this function do?"
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

          <div className="grid grid-cols-3 gap-4">
            <Field label="Language">
              <Select
                value={fn.language || "plpgsql"}
                onChange={(e) => updateFn((f) => ({ ...f, language: e.target.value }))}
              >
                {LANGUAGES.map((l) => (
                  <option key={l} value={l}>{l}</option>
                ))}
              </Select>
            </Field>
            <Field label="Volatility">
              <Select
                value={fn.volatility || "volatile"}
                onChange={(e) => updateFn((f) => ({ ...f, volatility: e.target.value }))}
              >
                {VOLATILITIES.map((v) => (
                  <option key={v} value={v}>{v}</option>
                ))}
              </Select>
            </Field>
            <Field label="Security">
              <Select
                value={fn.security || "invoker"}
                onChange={(e) => updateFn((f) => ({ ...f, security: e.target.value }))}
              >
                {SECURITIES.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </Select>
            </Field>
          </div>

          <Field label="Return Type">
            <Input
              mono
              value={fn.returns?.type || ""}
              onChange={(e) =>
                updateFn((f) => ({
                  ...f,
                  returns: { ...f.returns, type: e.target.value },
                }))
              }
              placeholder="void, int, setof posts, etc."
            />
          </Field>

          <Field label="Function Body">
            {/* The body slots into this fixed wrapper — a byte-faithful mirror
                of what the migrator emits (generateRPCFunction): one clause per
                line, $ub$ dollar-quoting. Every clause is derived from the live
                Definition fields, so each dropdown change is reflected here.
                Pasting full DDL into the editor is visibly wrong and rejected
                by validation. */}
            <div className="rounded-lg border border-border overflow-hidden">
              <div className="px-3 py-2 border-b border-border">
                <code className="block text-[11px] font-mono text-muted-foreground whitespace-pre-wrap">
                  {`CREATE OR REPLACE FUNCTION public."${name}"(${(fn.args || [])
                    .map((a) => `"${a.name}" ${a.type}`)
                    .join(", ")})\nRETURNS ${fn.returns?.type || "void"}\nLANGUAGE ${(
                    fn.language || "plpgsql"
                  ).toLowerCase()}\n${(fn.volatility || "volatile").toUpperCase()}\nSECURITY ${(
                    fn.security || "invoker"
                  ).toUpperCase()}\nAS $ub$`}
                </code>
              </div>
              <CodeEditor
                value={fn.body || ""}
                onChange={(val) => updateFn((f) => ({ ...f, body: val }))}
                language="sql"
                minHeight="160px"
              />
              <div className="px-3 py-1.5 border-t border-border">
                <code className="text-[11px] font-mono text-muted-foreground">$ub$;</code>
              </div>
            </div>
          </Field>
        </Section>

        <Section
          title="Arguments"
          icon={Braces}
          actions={
            <Button
              variant="dashed"
              size="sm"
              onClick={async () => {
                const argName = await dialog.prompt("Argument name:");
                if (!argName?.trim()) return;
                updateFn((f) => ({
                  ...f,
                  args: [...(f.args || []), { name: argName.trim(), type: "text", required: false }],
                }));
              }}
            >
              <Plus size={14} />
              Add Arg
            </Button>
          }
        >
          {args.length > 0 ? (
            <div className="space-y-2">
              {args.map((arg, idx) => (
                <Panel key={arg.name} className="flex items-center gap-3 px-3 py-2">
                  <span className="text-sm font-mono text-foreground min-w-[100px]">{arg.name}</span>
                  <Select
                    mono
                    inputSize="sm"
                    className="w-auto"
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
                  >
                    {POSTGRES_TYPES.map((t) => (
                      <option key={t} value={t}>{t}</option>
                    ))}
                  </Select>
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
                  <Button
                    variant="danger-ghost"
                    size="icon"
                    className="ml-auto"
                    aria-label={`Delete ${arg.name}`}
                    onClick={() =>
                      updateFn((f) => ({
                        ...f,
                        args: (f.args || []).filter((_, i) => i !== idx),
                      }))
                    }
                  >
                    <Trash2 size={13} />
                  </Button>
                </Panel>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No arguments defined.</p>
          )}
        </Section>

        {backend.capabilities.canTestRpc && (
          <Disclosure label="Test Function">
            <div className="space-y-3">
              {args.map((arg) => (
                <Field key={arg.name} label={arg.name}>
                  <Input
                    mono
                    value={testInputs[arg.name] || ""}
                    onChange={(e) =>
                      setTestInputs((prev) => ({ ...prev, [arg.name]: e.target.value }))
                    }
                  />
                </Field>
              ))}
              <Button onClick={runTest} loading={testLoading}>
                {!testLoading && <Play size={14} />}
                Run
              </Button>
              {testResult && (
                <Panel className="p-4 overflow-x-auto max-h-64 overflow-y-auto">
                  <pre className="text-xs font-mono text-foreground">{testResult}</pre>
                </Panel>
              )}
            </div>
          </Disclosure>
        )}
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
