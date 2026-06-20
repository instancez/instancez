import { useEffect, useState } from "react";
import { Box, Text } from "@chakra-ui/react";

type ToastState = {
  visible: boolean;
  variant: "success" | "error";
  source: string;
  message: string;
};

let setStateFn: ((s: ToastState) => void) | null = null;

export function showSaveToast(opts: { source: string }) {
  if (!setStateFn) return;
  setStateFn({ visible: true, variant: "success", source: opts.source, message: "" });
}

export function showSaveErrorToast(opts: { message: string }) {
  if (!setStateFn) return;
  setStateFn({ visible: true, variant: "error", source: "", message: opts.message });
}

export function SaveToast() {
  const [state, setState] = useState<ToastState>({
    visible: false, variant: "success", source: "", message: "",
  });

  useEffect(() => {
    setStateFn = setState;
    return () => { setStateFn = null; };
  }, []);

  useEffect(() => {
    if (!state.visible) return;
    const t = setTimeout(() => setState((s) => ({ ...s, visible: false })), 8000);
    return () => clearTimeout(t);
  }, [state.visible]);

  if (!state.visible) return null;

  if (state.variant === "error") {
    return (
      <Box
        role="status"
        position="fixed"
        bottom="6"
        right="6"
        maxW="md"
        borderRadius="xl"
        borderWidth="1px"
        bg="red.50"
        _dark={{ bg: "red.900/20" }}
        color="fg.error"
        boxShadow="lg"
        px="4"
        py="3"
        fontSize="sm"
        zIndex="50"
        className="animate-rise"
      >
        {state.message}
      </Box>
    );
  }

  return (
    <Box
      role="status"
      position="fixed"
      bottom="6"
      right="6"
      maxW="md"
      borderRadius="xl"
      borderWidth="1px"
      bg="bg.panel"
      color="fg"
      boxShadow="lg"
      px="4"
      py="3"
      fontSize="sm"
      zIndex="50"
      className="animate-rise"
    >
      <Box>
        Saved to <Box as="code" fontFamily="mono">{state.source}</Box>.
      </Box>
      <Text mt="1" fontSize="xs" color="fg.muted">
        Reminder: update your git source to match, or your next external update will revert this.
      </Text>
    </Box>
  );
}
