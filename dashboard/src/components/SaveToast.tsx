import { useEffect, useState } from "react";
import { Box, Text } from "@chakra-ui/react";

type ToastState = {
  visible: boolean;
  source: string;
};

let setStateFn: ((s: ToastState) => void) | null = null;

export function showSaveToast(opts: { source: string }) {
  if (!setStateFn) return;
  setStateFn({ visible: true, source: opts.source });
}

export function SaveToast() {
  const [state, setState] = useState<ToastState>({
    visible: false, source: "",
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
