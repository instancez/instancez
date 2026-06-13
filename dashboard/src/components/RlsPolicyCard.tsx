import { Trash2 } from "lucide-react";
import { Box, HStack, VStack } from "@chakra-ui/react";
import { Panel, Button, Field } from "./ui";
import { Checkbox } from "./Checkbox";
import { CodeEditor } from "./CodeEditor";
import { RLS_OPERATIONS } from "../lib/utils";
import type { RLSPolicy } from "../lib/types";

interface RlsPolicyCardProps {
  policy: RLSPolicy;
  onChange: (policy: RLSPolicy) => void;
  onDelete: () => void;
  /** Optional one-click expressions rendered under the check editor. */
  quickFills?: { label: string; expr: string }[];
}

/**
 * The single RLS policy editor, shared by table and storage-bucket config:
 * permissive/restrictive type, operation checkboxes, and the check
 * expression.
 */
export function RlsPolicyCard({
  policy,
  onChange,
  onDelete,
  quickFills,
}: RlsPolicyCardProps) {
  return (
    <Panel p="4">
      <VStack gap="3" align="stretch">
        <HStack alignItems="start" justify="space-between" gap="3">
          <VStack gap="3" align="start">
            <Field label="Type">
              <HStack gap="1">
                {(["permissive", "restrictive"] as const).map((t) => {
                  const isActive = (policy.type || "permissive") === t;
                  const activeStyles = t === "restrictive"
                    ? { bg: "orange.50", color: "orange.700", borderColor: "orange.300" }
                    : { bg: "blue.50", color: "blue.700", borderColor: "blue.300" };
                  return (
                    <Box
                      key={t}
                      as="button"
                      {...({ type: "button" } as object)}
                      onClick={() => onChange({ ...policy, type: t })}
                      px="2.5"
                      py="1"
                      borderRadius="md"
                      fontSize="xs"
                      fontWeight="medium"
                      transition="colors"
                      cursor="pointer"
                      borderWidth="1px"
                      bg={isActive ? activeStyles.bg : "transparent"}
                      color={isActive ? activeStyles.color : "fg.muted"}
                      borderColor={isActive ? activeStyles.borderColor : "border"}
                      _hover={isActive ? undefined : { color: "fg", bg: "bg.subtle" }}
                    >
                      {t}
                    </Box>
                  );
                })}
              </HStack>
            </Field>
            <Field label="Operations">
              <HStack gap="2">
                {RLS_OPERATIONS.map((op) => (
                  <Checkbox
                    key={op}
                    className="text-xs"
                    label={op}
                    checked={(policy.operations || []).includes(op)}
                    onChange={(c) =>
                      onChange({
                        ...policy,
                        operations: c
                          ? [...(policy.operations || []), op]
                          : (policy.operations || []).filter((o) => o !== op),
                      })
                    }
                  />
                ))}
              </HStack>
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
        <Field label="Check Expression">
          {/* The check is a boolean expression that slots into the generated
              policy DDL — framed here so pasting a full statement is visibly
              wrong (and rejected by validation). */}
          <Box borderRadius="lg" borderWidth="1px" overflow="hidden">
            <Box px="3" py="1.5" borderBottomWidth="1px">
              <Box as="code" display="block" fontSize="11px" fontFamily="mono" color="fg.muted">
                CREATE POLICY … FOR {(policy.operations || []).join(", ") || "…"} USING (
              </Box>
            </Box>
            <CodeEditor
              value={policy.check || ""}
              onChange={(val) => onChange({ ...policy, check: val })}
              placeholder="user_id = auth.uid()"
              minHeight="60px"
            />
            <Box px="3" py="1.5" borderTopWidth="1px">
              <Box as="code" fontSize="11px" fontFamily="mono" color="fg.muted">)</Box>
            </Box>
          </Box>
          {quickFills && quickFills.length > 0 && (
            <HStack gap="2" mt="2">
              {quickFills.map(({ label, expr }) => (
                <Button
                  key={label}
                  variant="outline"
                  size="xs"
                  onClick={() => onChange({ ...policy, check: expr })}
                >
                  {label}
                </Button>
              ))}
            </HStack>
          )}
        </Field>
      </VStack>
    </Panel>
  );
}
