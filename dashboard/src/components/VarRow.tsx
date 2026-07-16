import { useState } from "react";
import { Box, HStack, Text } from "@chakra-ui/react";
import { Input, Button } from "./ui";

export interface VarRowProps {
  /** Human-readable config name, e.g. "API key" */
  label: string;
  /** Env var that can supply this value, e.g. INSTANCEZ_RESEND_API_KEY */
  name: string;
  isSet: boolean;
  /** Last-4 tail of the current value, shown masked when set. Never the plaintext. */
  tail?: string;
  canWrite: boolean;
  inputValue: string;
  onInputChange: (value: string) => void;
  /** Whether the env var name caption is meaningful to this consumer (instance:
   *  true — self-hosters set it in their own .env; platform: false — the name
   *  is an internal storage detail the platform user cannot act on). */
  showEnvName: boolean;
}

/**
 * One provider config value: a label plus, when set, a masked tail of the
 * current value (••••1a2b, never the plaintext) with an explicit "Override"
 * affordance. When unset the write input shows directly. Secrets are
 * write-only, so the tail is the only read-back a user gets.
 */
export function VarRow({
  label,
  name,
  isSet,
  tail,
  canWrite,
  inputValue,
  onInputChange,
  showEnvName,
}: VarRowProps) {
  const [overriding, setOverriding] = useState(false);
  const showInput = canWrite && (!isSet || overriding || inputValue !== "");

  return (
    <Box py="2.5" spaceY="1.5">
      <HStack justify="space-between" gap="3">
        <Text as="span" fontSize="xs" fontWeight="medium" color="fg">{label}</Text>
        {!showInput && (
          <HStack as="span" gap="2">
            <Text as="span" flexShrink="0" fontSize="11px" fontFamily="mono" color="fg.muted">
              {isSet ? `••••${tail ?? ""}` : "not set"}
            </Text>
            {canWrite && isSet && (
              <Button variant="dashed" size="sm" onClick={() => setOverriding(true)}>
                Override
              </Button>
            )}
          </HStack>
        )}
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
      {showEnvName && (
        <Text fontSize="11px" color="fg.muted">
          env <Box as="code" fontFamily="mono">{name}</Box>
        </Text>
      )}
    </Box>
  );
}
