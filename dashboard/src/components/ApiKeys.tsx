import { useEffect, useState, type ReactNode } from "react";
import { Check, Copy, Eye, EyeOff, KeyRound } from "lucide-react";
import { Box, HStack, VStack } from "@chakra-ui/react";
import { useBackend, useApiBaseUrl } from "../console/BackendContext";
import { Section, useSurfaceBg } from "./ui";
import { StatusBadge } from "./StatusBadge";

export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable (insecure context) — nothing useful to do.
    }
  }

  return (
    <Box
      as="button"
      onClick={handleCopy}
      aria-label={label}
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

/** The publishable anon key, fetched once from /api/_admin/keys (null until loaded or when unavailable). */
export function useAnonKey(): string | null {
  const backend = useBackend();
  const [anonKey, setAnonKey] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const keys = await backend.getKeys();
        if (!cancelled) setAnonKey(keys.anon_key);
      } catch {
        // Older backend without /keys — callers hide or use a placeholder.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [backend]);

  return anonKey;
}

interface KeyRowProps {
  label: string;
  badge?: ReactNode;
  value: string;
  secret?: boolean;
}

function KeyRow({ label, badge, value, secret }: KeyRowProps) {
  const [revealed, setRevealed] = useState(false);
  const hidden = secret && !revealed;

  return (
    <HStack gap="3" px="4" py="2.5">
      <HStack as="span" flexShrink="0" w="24" gap="2" fontSize="xs" fontWeight="medium" color="fg">
        {label}
        {badge}
      </HStack>
      <Box
        as="code"
        minW="0"
        flex="1"
        overflow="hidden"
        textOverflow="ellipsis"
        whiteSpace="nowrap"
        fontSize="xs"
        fontFamily="mono"
        color="fg.muted"
      >
        {hidden ? "•".repeat(40) : value}
      </Box>
      {secret && (
        <Box
          as="button"
          onClick={() => setRevealed((r) => !r)}
          aria-label={revealed ? `Hide ${label}` : `Reveal ${label}`}
          flexShrink="0"
          p="1.5"
          borderRadius="md"
          color="fg.muted"
          _hover={{ color: "fg", bg: "bg.subtle" }}
          transition="colors"
          cursor="pointer"
        >
          {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
        </Box>
      )}
      <CopyButton value={value} label={`Copy ${label}`} />
    </HStack>
  );
}

/**
 * Compact Settings → API panel: one line per key. anon is browser-safe and
 * runs under RLS; admin is full service_role and must stay server-side.
 */
export function ApiKeys() {
  const bg = useSurfaceBg();
  const anonKey = useAnonKey();
  const adminKey = sessionStorage.getItem("instancez_admin_key") || "";
  const apiUrl = useApiBaseUrl();

  return (
    <>
      <Section title="API Keys" icon={KeyRound}>
        <VStack bg={bg} borderRadius="xl" borderWidth="1px" gap="0" align="stretch" divideY="1px">
          <KeyRow label="API URL" value={apiUrl} />
          {anonKey !== null && anonKey !== "" && (
            <KeyRow
              label="anon"
              badge={<StatusBadge variant="info">public</StatusBadge>}
              value={anonKey}
            />
          )}
          {anonKey === "" && (
            <HStack px="4" py="2.5" gap="3">
              <HStack as="span" flexShrink="0" w="24" gap="2" fontSize="xs" fontWeight="medium" color="fg">
                anon
                <StatusBadge variant="info">public</StatusBadge>
              </HStack>
              <Box fontSize="xs" color="fg.muted" fontStyle="italic">
                Publish to get your anon key
              </Box>
            </HStack>
          )}
          {adminKey && (
            <KeyRow
              label="admin"
              badge={<StatusBadge variant="error">secret</StatusBadge>}
              value={adminKey}
              secret
            />
          )}
        </VStack>
      </Section>
    </>
  );
}
