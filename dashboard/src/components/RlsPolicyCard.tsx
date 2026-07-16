import { Trash2 } from "lucide-react";
import { Box, HStack, VStack } from "@chakra-ui/react";
import { Panel, Button, Field } from "./ui";
import { CodeEditor } from "./CodeEditor";
import { RLS_OPERATIONS } from "../lib/utils";
import type { RLSPolicy } from "../lib/types";

interface RlsPolicyCardProps {
  policy: RLSPolicy;
  onChange: (policy: RLSPolicy) => void;
  onDelete: () => void;
}

/** A single-select pill-button group (radio semantics, styled like tabs). */
function PillRadioGroup<T extends string>({
  options,
  value,
  onChange,
  activeStyles,
}: {
  options: readonly { value: T; label: string }[];
  value: T | null;
  onChange: (v: T) => void;
  activeStyles?: (v: T) => { bg: string; color: string; borderColor: string };
}) {
  const defaultActive = { bg: "blue.50", color: "blue.700", borderColor: "blue.300" };
  return (
    <HStack gap="1" role="radiogroup">
      {options.map(({ value: v, label }) => {
        const isActive = value === v;
        const styles = activeStyles ? activeStyles(v) : defaultActive;
        return (
          <Box
            key={v}
            as="button"
            {...({ type: "button", role: "radio", "aria-checked": isActive } as object)}
            onClick={() => onChange(v)}
            px="2.5"
            py="1"
            borderRadius="md"
            fontSize="xs"
            fontWeight="medium"
            transition="colors"
            cursor="pointer"
            borderWidth="1px"
            bg={isActive ? styles.bg : "transparent"}
            color={isActive ? styles.color : "fg.muted"}
            borderColor={isActive ? styles.borderColor : "border"}
            _hover={isActive ? undefined : { color: "fg", bg: "bg.subtle" }}
          >
            {label}
          </Box>
        );
      })}
    </HStack>
  );
}

const OPERATION_OPTIONS = [
  ...RLS_OPERATIONS.map((op) => ({ value: op as string, label: op.toUpperCase() })),
  { value: "all", label: "ALL" },
] as const;

/** "all" if operations is exactly the 4 CRUD ops, the single op if there's just one, else null (custom combo, not representable by the radio group). */
function operationsToRadioValue(ops: string[]): string | null {
  if (ops.length === RLS_OPERATIONS.length && RLS_OPERATIONS.every((o) => ops.includes(o))) {
    return "all";
  }
  if (ops.length === 1) return ops[0] ?? null;
  return null;
}

/**
 * The single RLS policy editor, shared by table and storage-bucket config:
 * permissive/restrictive type, single-select operation radio (with an ALL
 * shortcut), and the using / with check expressions (shown or hidden based
 * on the selected operation — UPDATE and ALL show both).
 */
export function RlsPolicyCard({
  policy,
  onChange,
  onDelete,
}: RlsPolicyCardProps) {
  return (
    <Panel p="4">
      <VStack gap="3" align="stretch">
        <HStack alignItems="start" justify="space-between" gap="3">
          <VStack gap="3" align="start">
            <Field label="Type">
              <PillRadioGroup
                options={[
                  { value: "permissive", label: "permissive" },
                  { value: "restrictive", label: "restrictive" },
                ] as const}
                value={policy.type || "permissive"}
                onChange={(t) => onChange({ ...policy, type: t })}
                activeStyles={(t) =>
                  t === "restrictive"
                    ? { bg: "orange.50", color: "orange.700", borderColor: "orange.300" }
                    : { bg: "blue.50", color: "blue.700", borderColor: "blue.300" }
                }
              />
            </Field>
            <Field label="Operations">
              <PillRadioGroup
                options={OPERATION_OPTIONS}
                value={operationsToRadioValue(policy.operations || [])}
                onChange={(v) =>
                  onChange({
                    ...policy,
                    operations: v === "all" ? [...RLS_OPERATIONS] : [v],
                  })
                }
              />
            </Field>
          </VStack>
          <Button
            variant="danger-ghost"
            size="icon"
            aria-label="Delete policy"
            onClick={onDelete}
          >
            <Trash2 size={14} />
          </Button>
        </HStack>
        {(policy.operations || []).some((op) => op === "select" || op === "update" || op === "delete") && (
          <Field
            label="Using"
            hint="Available: auth.uid(), auth.role(), auth.email(), auth.jwt(), auth.is_authenticated()"
          >
            <Box borderRadius="lg" borderWidth="1px" overflow="hidden">
              <CodeEditor
                value={policy.using || ""}
                onChange={(val) => onChange({ ...policy, using: val })}
                placeholder="user_id = auth.uid()"
                minHeight="60px"
                frame={{
                  header: `CREATE POLICY … FOR ${(policy.operations || []).join(", ") || "…"} USING (`,
                  footer: ")",
                }}
              />
            </Box>
          </Field>
        )}
        {(policy.operations || []).some((op) => op === "insert" || op === "update") && (
          <Field
            label="With Check"
            hint="Available: auth.uid(), auth.role(), auth.email(), auth.jwt(), auth.is_authenticated()"
          >
            <Box borderRadius="lg" borderWidth="1px" overflow="hidden">
              <CodeEditor
                value={policy.with_check || ""}
                onChange={(val) => onChange({ ...policy, with_check: val })}
                placeholder="user_id = auth.uid()"
                minHeight="60px"
                frame={{
                  header: `CREATE POLICY … FOR ${(policy.operations || []).join(", ") || "…"} WITH CHECK (`,
                  footer: ")",
                }}
              />
            </Box>
          </Field>
        )}
      </VStack>
    </Panel>
  );
}
