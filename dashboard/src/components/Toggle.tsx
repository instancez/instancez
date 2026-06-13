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

/**
 * Toggle is the single switch control used across the dashboard for boolean
 * settings. It renders an accessible `role="switch"` button; pass `label` to
 * get the standard "switch + text" row, or omit it for a bare switch (e.g. in
 * a table cell, where you should pass `aria-label`).
 */
export function Toggle({ checked, onChange, label, disabled, "aria-label": ariaLabel }: ToggleProps) {
  if (label == null) {
    return (
      <SwitchWidget checked={checked} onChange={onChange} disabled={disabled} ariaLabel={ariaLabel} />
    );
  }
  return (
    <HStack gap="3" fontSize="sm" color="fg" cursor={disabled ? "not-allowed" : "pointer"}>
      <SwitchWidget checked={checked} onChange={onChange} disabled={disabled} />
      <Text onClick={() => !disabled && onChange(!checked)}>{label}</Text>
    </HStack>
  );
}
