import { useState } from "react";
import { Box, HStack, Text } from "@chakra-ui/react";
import { Input, Button } from "./ui";

export interface VarRowProps {
  /** Human-readable config name, e.g. "API key" */
  label: string;
  /** Env var that can supply this value, e.g. INSTANCEZ_RESEND_API_KEY */
  name: string;
  isSet: boolean;
  canWrite: boolean;
  inputValue: string;
  onInputChange: (value: string) => void;
}

/**
 * One provider config value: label + set/unset status, the env var that can
 * supply the value as a caption, and a write input. When the var is already
 * set, the input hides behind an explicit "Override" affordance so it's clear
 * a staged value replaces the current one.
 */
export function VarRow({ label, name, isSet, canWrite, inputValue, onInputChange }: VarRowProps) {
  const [overriding, setOverriding] = useState(false);
  const showInput = canWrite && (!isSet || overriding || inputValue !== "");

  return (
    <Box py="2.5" spaceY="1.5">
      <HStack justify="space-between" gap="3">
        <Text as="span" fontSize="xs" fontWeight="medium" color="fg">{label}</Text>
        <HStack as="span" gap="2">
          <Text
            as="span"
            flexShrink="0"
            fontSize="11px"
            fontWeight="medium"
            color={isSet ? "green.600" : "fg.error"}
          >
            {isSet ? "✓ set" : "✗ unset"}
          </Text>
          {canWrite && isSet && !showInput && (
            <Button variant="dashed" size="sm" onClick={() => setOverriding(true)}>
              Override
            </Button>
          )}
        </HStack>
      </HStack>
      {showInput && (
        <HStack gap="2">
          <Box flex="1">
            <Input
              type="password"
              aria-label={name}
              placeholder={isSet ? "new value — overrides the current one…" : "enter value…"}
              value={inputValue}
              onChange={(e) => onInputChange(e.target.value)}
            />
          </Box>
          {isSet && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setOverriding(false);
                onInputChange("");
              }}
            >
              Keep current
            </Button>
          )}
        </HStack>
      )}
      <Text fontSize="11px" color="fg.muted">
        env <Box as="code" fontFamily="mono">{name}</Box>
      </Text>
    </Box>
  );
}
