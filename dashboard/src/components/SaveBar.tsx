import { createPortal } from "react-dom";
import { Save } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { Button } from "./ui";
import { useSaveBarDock } from "./saveBarDock";
import type { ValidationError } from "../lib/types";

interface SaveBarProps {
  onSave: () => void;
  saving: boolean;
  errors: ValidationError[];
  dirty?: boolean;
}

export function SaveBar({ onSave, saving, errors, dirty = true }: SaveBarProps) {
  const dockNode = useSaveBarDock();

  if (!dirty && errors.length === 0) return null;

  // Docked footer. The Layout gives us the slot below the scroll region, so the
  // bar reserves its own row at the bottom of the card instead of floating over
  // the content. It sits outside the surface context, so its background is set
  // explicitly. With no dock (unit tests) it renders inline.
  const bar = (
    <Box borderTopWidth="1px" bg="bg.panel" px="8" py="3">
      <HStack justify="space-between" gap="4">
        <Box flex="1" minW="0">
          {errors.length > 0 && (
            <VStack gap="1" align="start">
              {errors.slice(0, 3).map((err, i) => (
                <Text key={i} fontSize="xs" fontFamily="mono" color="fg.error" truncate>
                  {err.path && <Box as="span" fontWeight="medium">{err.path}: </Box>}
                  {err.message}
                  {err.suggestion && <Box as="span" color="fg.muted"> — {err.suggestion}</Box>}
                </Text>
              ))}
              {errors.length > 3 && (
                <Text fontSize="xs" color="fg.muted">+{errors.length - 3} more errors</Text>
              )}
            </VStack>
          )}
        </Box>
        <Button onClick={onSave} loading={saving}>
          {!saving && <Save size={14} />}
          {saving ? "Saving..." : "Save Changes"}
        </Button>
      </HStack>
    </Box>
  );

  return dockNode ? createPortal(bar, dockNode) : bar;
}
