import { Check } from "lucide-react";
import type { ReactNode } from "react";
import { Box, HStack, Text } from "@chakra-ui/react";

type CheckboxProps = {
  checked: boolean;
  onChange: (checked: boolean) => void;
  /** Text rendered to the right of the box; clicking it also toggles. */
  label?: ReactNode;
  /** Extra classes for the wrapper — use to set the label's text size. */
  className?: string;
  "aria-label"?: string;
  disabled?: boolean;
};

/**
 * Checkbox is the styled multi-select control used where a Toggle would be the
 * wrong mental model — i.e. "pick which of N" rather than a single on/off
 * setting (e.g. RLS operations). For single booleans use Toggle.
 */
export function Checkbox({ checked, onChange, label, className, disabled, "aria-label": ariaLabel }: CheckboxProps) {
  const box = (
    <Box
      asChild
      display="flex"
      alignItems="center"
      justifyContent="center"
      w="18px"
      h="18px"
      borderRadius="md"
      borderWidth="1px"
      transition="colors"
      flexShrink="0"
      opacity={disabled ? 0.5 : 1}
      cursor={disabled ? "not-allowed" : "pointer"}
      bg={checked ? "fg" : "transparent"}
      borderColor={checked ? "fg" : "border"}
      color={checked ? "bg" : undefined}
    >
      <button
        type="button"
        role="checkbox"
        aria-checked={checked}
        aria-label={ariaLabel}
        disabled={disabled}
        onClick={() => onChange(!checked)}
      >
        {checked && <Check size={12} strokeWidth={3} />}
      </button>
    </Box>
  );

  if (label == null) return box;

  return (
    <HStack
      as="span"
      gap="2"
      color="fg"
      display="inline-flex"
      alignItems="center"
      className={className}
    >
      {box}
      <Text
        as="span"
        cursor={disabled ? undefined : "pointer"}
        onClick={() => !disabled && onChange(!checked)}
      >
        {label}
      </Text>
    </HStack>
  );
}
