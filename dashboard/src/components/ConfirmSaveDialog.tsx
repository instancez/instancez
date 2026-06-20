import { useMemo, useState } from "react";
import { structuredPatch } from "diff";
import { FileDiff, X } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { Button, Input } from "./ui";

/** Word the user must type to arm the Confirm button. */
const CONFIRM_PHRASE = "CONFIRM";

export interface DotenvChange {
  /** Env var name being written to .env */
  name: string;
  /** Last 4 characters of the new value, for recognition without disclosure */
  tail: string;
  /** True when the var is already set and this overwrites it */
  isUpdate: boolean;
}

interface ConfirmSaveDialogProps {
  current: string;
  proposed: string;
  dotenvChanges: DotenvChange[];
  saving: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

type LineStyle = { color: string; bg?: string };

function lineStyle(line: string): LineStyle {
  if (line.startsWith("+")) return { color: "green.700", bg: "green.50" };
  if (line.startsWith("-")) return { color: "red.600", bg: "red.50" };
  return { color: "fg.muted" };
}

/**
 * Pre-save review: a unified diff of instancez.yaml plus the staged .env
 * writes (values masked to a last-4 tail). Nothing is applied until Confirm.
 */
export function ConfirmSaveDialog({
  current,
  proposed,
  dotenvChanges,
  saving,
  onConfirm,
  onCancel,
}: ConfirmSaveDialogProps) {
  const hunks = useMemo(
    () => structuredPatch("instancez.yaml", "instancez.yaml", current, proposed, "", "", { context: 3 }).hunks,
    [current, proposed]
  );

  const [phrase, setPhrase] = useState("");
  const armed = phrase.trim().toUpperCase() === CONFIRM_PHRASE;

  return (
    <Box position="fixed" inset="0" zIndex="50" display="flex" alignItems="center" justifyContent="center" p="6">
      <Box position="absolute" inset="0" bg="blackAlpha.400" onClick={onCancel} />
      <Box
        position="relative"
        w="full"
        maxW="2xl"
        maxH="80vh"
        display="flex"
        flexDir="column"
        borderRadius="xl"
        borderWidth="1px"
        bg="bg.panel"
        boxShadow="lg"
      >
        <HStack justify="space-between" px="5" py="3" borderBottomWidth="1px">
          <HStack as="span" gap="2" fontSize="sm" fontWeight="medium" color="fg">
            <FileDiff size={15} />
            <Text>Review changes before saving</Text>
          </HStack>
          <Button variant="ghost" size="icon" aria-label="Close" onClick={onCancel}>
            <X size={14} />
          </Button>
        </HStack>

        <VStack flex="1" minH="0" overflowY="auto" px="5" py="4" gap="4" align="stretch">
          <Box as="section">
            <Text fontSize="xs" fontWeight="medium" color="fg" mb="2">instancez.yaml</Text>
            {hunks.length === 0 ? (
              <Text fontSize="xs" color="fg.muted" fontStyle="italic">No changes</Text>
            ) : (
              <Box
                as="pre"
                borderRadius="lg"
                borderWidth="1px"
                bg="bg"
                fontSize="11px"
                fontFamily="mono"
                lineHeight="1.5"
                overflowX="auto"
              >
                {hunks.map((hunk, hi) => (
                  <Box key={hi} borderTopWidth={hi > 0 ? "1px" : undefined}>
                    <Box px="3" color="fg.muted" userSelect="none">
                      @@ -{hunk.oldStart},{hunk.oldLines} +{hunk.newStart},{hunk.newLines} @@
                    </Box>
                    {hunk.lines.map((line, li) => {
                      const s = lineStyle(line);
                      return (
                        <Box key={li} px="3" whiteSpace="pre" color={s.color} bg={s.bg}>
                          {line}
                        </Box>
                      );
                    })}
                  </Box>
                ))}
              </Box>
            )}
          </Box>

          {dotenvChanges.length > 0 && (
            <Box as="section">
              <Text fontSize="xs" fontWeight="medium" color="fg" mb="2">.env</Text>
              <VStack
                borderRadius="lg"
                borderWidth="1px"
                bg="bg"
                gap="0"
                align="stretch"
                divideY="1px"
              >
                {dotenvChanges.map((change) => (
                  <HStack key={change.name} gap="3" px="3" py="2">
                    <Box
                      as="code"
                      flex="1"
                      minW="0"
                      fontSize="11px"
                      fontFamily="mono"
                      color="fg"
                      overflow="hidden"
                      textOverflow="ellipsis"
                      whiteSpace="nowrap"
                    >
                      {change.name}=<Box as="span" color="fg.muted">••••{change.tail}</Box>
                    </Box>
                    <Text
                      as="span"
                      flexShrink="0"
                      fontSize="11px"
                      fontWeight="medium"
                      color={change.isUpdate ? "orange.600" : "green.600"}
                    >
                      {change.isUpdate ? "updated" : "added"}
                    </Text>
                  </HStack>
                ))}
              </VStack>
            </Box>
          )}
        </VStack>

        <HStack justify="space-between" gap="3" px="5" py="3" borderTopWidth="1px">
          <HStack gap="2" flex="1" minW="0">
            <Box as="label" {...({ htmlFor: "confirm-phrase" } as object)} fontSize="xs" color="fg.muted" whiteSpace="nowrap">
              Type <Box as="code" fontFamily="mono" color="fg">{CONFIRM_PHRASE}</Box> to apply
            </Box>
            <Box w="40" flexShrink="0">
              <Input
                id="confirm-phrase"
                inputSize="sm"
                mono
                value={phrase}
                onChange={(e) => setPhrase(e.target.value)}
                placeholder={CONFIRM_PHRASE}
                autoFocus
                disabled={saving}
              />
            </Box>
          </HStack>
          <HStack gap="2" flexShrink="0">
            <Button variant="ghost" onClick={onCancel} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={onConfirm} loading={saving} disabled={!armed}>
              {saving ? "Saving..." : "Confirm & Save"}
            </Button>
          </HStack>
        </HStack>
      </Box>
    </Box>
  );
}
