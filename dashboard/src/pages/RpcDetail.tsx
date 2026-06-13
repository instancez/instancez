import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { Trash2, Plus, FileCode2, Braces } from "lucide-react";
import { Box, Grid, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { jsonEqual } from "../lib/jsonEqual";
import { useDialog } from "../components/Dialog";
import { DetailToolbar } from "../components/DetailToolbar";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { Toggle } from "../components/Toggle";
import {
  Button,
  Field,
  Input,
  Panel,
  Section,
  Select,
} from "../components/ui";
import { POSTGRES_TYPES } from "../lib/utils";
import type { RpcFunction } from "../lib/types";

const LANGUAGES = ["plpgsql", "sql"];
const VOLATILITIES = ["volatile", "stable", "immutable"];
const SECURITIES = ["invoker", "definer"];

export function RpcDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [fn, setFn] = useState<RpcFunction | null>(null);

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

  if (!config || !fn || !name) {
    return (
      <Box p="8">
        <Text fontSize="sm" color="fg.muted">Function not found.</Text>
      </Box>
    );
  }

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty = !jsonEqual(fn, (config.rpc || {})[name] ?? null);

  const args = fn.args || [];

  return (
    <Box pb="20">
      <DetailToolbar backLabel="Database Functions" onDelete={deleteFunction} />
      <VStack pb="8" gap="6" maxW="3xl" align="stretch">
        <Section
          title="Definition"
          icon={FileCode2}
        >
          <Grid gridTemplateColumns="repeat(2, 1fr)" gap="4">
            <Field label="Description">
              <Input
                value={fn.description}
                onChange={(e) => updateFn((f) => ({ ...f, description: e.target.value }))}
                placeholder="What does this function do?"
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

          <Grid gridTemplateColumns="repeat(3, 1fr)" gap="4">
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
          </Grid>

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
            <Box borderRadius="lg" borderWidth="1px" borderColor="border" overflow="hidden">
              <Box px="3" py="2" borderBottomWidth="1px" borderColor="border">
                <Text
                  as="code"
                  display="block"
                  fontSize="11px"
                  fontFamily="mono"
                  color="fg.muted"
                  whiteSpace="pre-wrap"
                >
                  {`CREATE OR REPLACE FUNCTION public."${name}"(${(fn.args || [])
                    .map((a) => `"${a.name}" ${a.type}`)
                    .join(", ")})\nRETURNS ${fn.returns?.type || "void"}\nLANGUAGE ${(
                    fn.language || "plpgsql"
                  ).toLowerCase()}\n${(fn.volatility || "volatile").toUpperCase()}\nSECURITY ${(
                    fn.security || "invoker"
                  ).toUpperCase()}\nAS $ub$`}
                </Text>
              </Box>
              <CodeEditor
                value={fn.body || ""}
                onChange={(val) => updateFn((f) => ({ ...f, body: val }))}
                language="sql"
                minHeight="160px"
              />
              <Box px="3" py="1.5" borderTopWidth="1px" borderColor="border">
                <Text as="code" fontSize="11px" fontFamily="mono" color="fg.muted">$ub$;</Text>
              </Box>
            </Box>
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
            <VStack gap="2" align="stretch">
              {args.map((arg, idx) => (
                <Panel key={arg.name} display="flex" alignItems="center" gap="3" px="3" py="2">
                  <Text fontSize="sm" fontFamily="mono" color="fg" minW="100px">{arg.name}</Text>
                  <Select
                    mono
                    inputSize="sm"
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
                    ml="auto"
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
            </VStack>
          ) : (
            <Text fontSize="sm" color="fg.muted">No arguments defined.</Text>
          )}
        </Section>

      </VStack>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </Box>
  );
}
