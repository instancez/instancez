import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { Box } from "@chakra-ui/react";

/** The one copy button: shows a check for 1.5s after a successful copy. */
export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      return;
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1500);
  }

  return (
    <Box
      as="button"
      onClick={handleCopy}
      aria-label={`Copy ${label}`}
      title={copied ? "Copied!" : `Copy ${label}`}
      flexShrink="0"
      p="1.5"
      borderRadius="md"
      color="fg.muted"
      _hover={{ color: "fg", bg: "bg.subtle" }}
      transition="colors"
      cursor="pointer"
    >
      {copied ? <Check size={14} /> : <Copy size={14} />}
    </Box>
  );
}
