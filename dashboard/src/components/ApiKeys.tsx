import { useEffect, useState, type ReactNode } from "react";
import { Check, Copy, Eye, EyeOff, KeyRound } from "lucide-react";
import { useBackend } from "../console/BackendContext";
import { Section, useSurfaceBg } from "./ui";
import { StatusBadge } from "./StatusBadge";
import { cn } from "../lib/utils";

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
    <button
      onClick={handleCopy}
      aria-label={label}
      className="shrink-0 p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
    >
      {copied ? <Check size={14} /> : <Copy size={14} />}
    </button>
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
    <div className="flex items-center gap-3 px-4 py-2.5">
      <span className="shrink-0 w-24 flex items-center gap-2 text-xs font-medium text-foreground">
        {label}
        {badge}
      </span>
      <code className="min-w-0 flex-1 truncate text-xs font-mono text-muted-foreground">
        {hidden ? "•".repeat(40) : value}
      </code>
      {secret && (
        <button
          onClick={() => setRevealed((r) => !r)}
          aria-label={revealed ? `Hide ${label}` : `Reveal ${label}`}
          className="shrink-0 p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
        >
          {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
        </button>
      )}
      <CopyButton value={value} label={`Copy ${label}`} />
    </div>
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

  return (
    <Section title="API Keys" icon={KeyRound}>
      <div className={cn(bg, "rounded-xl border border-border divide-y divide-border")}>
        <KeyRow label="API URL" value={window.location.origin} />
        {anonKey !== null && (
          <KeyRow
            label="anon"
            badge={<StatusBadge variant="info">public</StatusBadge>}
            value={anonKey}
          />
        )}
        {adminKey && (
          <KeyRow
            label="admin"
            badge={<StatusBadge variant="error">secret</StatusBadge>}
            value={adminKey}
            secret
          />
        )}
      </div>
    </Section>
  );
}
