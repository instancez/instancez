import { useEffect, useState, type ReactNode } from "react";
import { Check, Copy, Eye, EyeOff, KeyRound } from "lucide-react";
import { getKeys } from "../api/client";
import { Section, useSurfaceBg } from "./ui";
import { StatusBadge } from "./StatusBadge";
import { cn } from "../lib/utils";

function CopyButton({ value, label }: { value: string; label: string }) {
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

interface KeyRowProps {
  label: string;
  badge?: ReactNode;
  description: string;
  value: string;
  secret?: boolean;
}

function KeyRow({ label, badge, description, value, secret }: KeyRowProps) {
  const bg = useSurfaceBg();
  const [revealed, setRevealed] = useState(false);
  const hidden = secret && !revealed;

  return (
    <div className={cn(bg, "rounded-xl border border-border px-5 py-3.5")}>
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-foreground">{label}</span>
        {badge}
      </div>
      <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
      <div className="mt-2 flex items-center gap-2">
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
    </div>
  );
}

/**
 * Supabase-style Settings → API panel: project URL, the publishable anon key
 * (fetched from /api/_admin/keys) and the admin key (already in
 * sessionStorage from login — the server never echoes it).
 */
export function ApiKeys() {
  const [anonKey, setAnonKey] = useState<string | null>(null);
  const adminKey = sessionStorage.getItem("instancez_admin_key") || "";

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const keys = await getKeys();
        if (!cancelled) setAnonKey(keys.anon_key);
      } catch {
        // Older backend without /keys — hide the anon row rather than error.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <Section
      title="API Keys"
      icon={KeyRound}
      description="Connect a supabase-js client to this project"
    >
      <KeyRow
        label="API URL"
        description="Pass as the first argument to createClient()"
        value={window.location.origin}
      />
      {anonKey !== null && (
        <KeyRow
          label="anon"
          badge={<StatusBadge variant="info">public</StatusBadge>}
          description="Safe to use in a browser — requests run as the anon role under your RLS policies"
          value={anonKey}
        />
      )}
      {adminKey && (
        <KeyRow
          label="admin"
          badge={<StatusBadge variant="error">secret</StatusBadge>}
          description="Full service_role access, bypasses Row Level Security — never ship it to a browser"
          value={adminKey}
          secret
        />
      )}
    </Section>
  );
}
