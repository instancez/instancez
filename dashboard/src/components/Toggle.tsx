import React from "react";
import { HStack, SwitchRoot, SwitchControl, SwitchThumb, SwitchHiddenInput, Text } from "@chakra-ui/react";

type ToggleProps = {
  checked: boolean;
  onChange: (checked: boolean) => void;
  /** Optional text rendered to the right of the switch; clicking it also toggles. */
  label?: React.ReactNode;
  "aria-label"?: string;
  disabled?: boolean;
};

function StateText({ on }: { on: boolean }) {
  return (
    <Text as="span" aria-hidden fontSize="xs" fontWeight="medium" color="fg.muted" minW="6">
      {on ? "On" : "Off"}
    </Text>
  );
}

function SwitchWidget({
  checked,
  onChange,
  disabled,
  ariaLabel,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  ariaLabel?: string;
}) {
  return (
    <SwitchRoot
      checked={checked}
      onCheckedChange={(e: { checked: boolean }) => onChange(e.checked)}
      disabled={disabled}
      aria-label={ariaLabel}
      colorPalette="gray"
      size="sm"
    >
      <SwitchHiddenInput role="switch" />
      <SwitchControl>
        <SwitchThumb />
      </SwitchControl>
    </SwitchRoot>
  );
}

/** Boolean switch with a visible On/Off word; pass `label` for the clickable text row, else `aria-label`. */
export function Toggle({ checked, onChange, label, disabled, "aria-label": ariaLabel }: ToggleProps) {
  if (label == null) {
    return (
      <HStack gap="2">
        <SwitchWidget checked={checked} onChange={onChange} disabled={disabled} ariaLabel={ariaLabel} />
        <StateText on={checked} />
      </HStack>
    );
  }
  return (
    <HStack gap="3" fontSize="sm" color="fg" cursor={disabled ? "not-allowed" : "pointer"}>
      <SwitchWidget checked={checked} onChange={onChange} disabled={disabled} />
      <Text onClick={() => !disabled && onChange(!checked)}>{label}</Text>
      <StateText on={checked} />
    </HStack>
  );
}
