import { Save } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { Button } from "./ui";
import type { ValidationError } from "../lib/types";

interface SaveBarProps {
  onSave: () => void;
  saving: boolean;
  errors: ValidationError[];
  dirty?: boolean;
}

export function SaveBar({ onSave, saving, errors, dirty = true }: SaveBarProps) {
  if (!dirty && errors.length === 0) return null;

  // Floats inside the content card: 8px page margin + 240px sidebar +
  // 8px gap + 16px inset = 272px from the viewport's left edge.
  return (
    <Box
      position="fixed"
      bottom="6"
      left="272px"
      right="6"
      zIndex="30"
      borderRadius="xl"
      borderWidth="1px"
      bg="bg.panel"
      boxShadow="lg"
      px="5"
      py="3"
      className="animate-rise"
    >
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
}
