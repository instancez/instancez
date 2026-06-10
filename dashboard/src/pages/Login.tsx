import { useState } from "react";
import { KeyRound, Loader2, AlertCircle } from "lucide-react";
import { validateAdminKey } from "../api/client";
import { Logo } from "../components/Logo";

interface LoginProps {
  onSuccess: () => void;
}

export function Login({ onSuccess }: LoginProps) {
  const [key, setKey] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!key.trim()) return;

    setLoading(true);
    setError("");

    const valid = await validateAdminKey(key.trim());
    if (valid) {
      sessionStorage.setItem("instancez_admin_key", key.trim());
      onSuccess();
    } else {
      setError("Invalid admin key. Check INSTANCEZ_ADMIN_KEY.");
    }
    setLoading(false);
  }

  return (
    <div className="min-h-dvh bg-background grid-canvas flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="relative frame-ticks border border-border bg-surface px-8 pt-10 pb-8">
          {/* Logo */}
          <div className="flex flex-col items-center mb-8">
            <Logo size={48} className="mb-5" />
            <p className="t-label mb-2">Restricted access</p>
            <h1 className="text-lg font-semibold tracking-tight text-foreground">
              instancez dashboard
            </h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Enter your admin key to continue
            </p>
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label htmlFor="admin-key" className="t-label block mb-2">
                Admin Key
              </label>
              <div className="relative">
                <KeyRound
                  size={16}
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground"
                />
                <input
                  id="admin-key"
                  type="password"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                  placeholder="INSTANCEZ_ADMIN_KEY"
                  autoFocus
                  className="w-full pl-10 pr-4 py-2.5 border border-input-border bg-input font-mono text-sm text-foreground placeholder:text-muted-foreground/50 focus:outline-none focus:border-ring transition-colors"
                />
              </div>
            </div>

            {error && (
              <div className="flex items-center gap-2 p-3 border border-destructive/40 bg-destructive/10">
                <AlertCircle size={14} className="text-destructive shrink-0" />
                <p className="text-xs text-destructive">{error}</p>
              </div>
            )}

            <button
              type="submit"
              disabled={loading || !key.trim()}
              className="w-full py-2.5 bg-accent text-background text-sm font-medium hover:bg-accent-hover transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed flex items-center justify-center gap-2"
            >
              {loading && <Loader2 size={14} className="animate-spin" />}
              {loading ? "Verifying..." : "Continue"}
            </button>
          </form>
        </div>

        <p className="mt-6 text-center text-xs text-muted-foreground">
          The admin key is stored in{" "}
          <span className="font-mono">sessionStorage</span> and cleared when you
          close the tab.
        </p>
      </div>
    </div>
  );
}
