import { useState } from "react";
import { KeyRound, AlertCircle } from "lucide-react";
import { validateAdminKey } from "../api/client";
import { Logo } from "../components/Logo";
import { Button, Field, Input } from "../components/ui";

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
    <div className="min-h-dvh bg-background flex items-center justify-center px-4">
      <div className="w-full max-w-md animate-rise">
        <div className="bg-surface border border-border rounded-2xl shadow-card px-8 pt-10 pb-8">
          {/* Brand */}
          <div className="flex flex-col items-center mb-8 text-center">
            <Logo size={56} className="mb-6" />
            <h1 className="text-2xl font-bold tracking-tight text-foreground">
              Welcome back
            </h1>
            <p className="mt-1.5 text-sm text-muted-foreground">
              Enter your admin key to open the dashboard
            </p>
          </div>

          <form onSubmit={handleSubmit} className="space-y-5">
            <Field label="Admin Key" htmlFor="admin-key">
              <div className="relative">
                <KeyRound
                  size={16}
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground"
                />
                <Input
                  mono
                  id="admin-key"
                  type="password"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                  placeholder="INSTANCEZ_ADMIN_KEY"
                  autoFocus
                  className="pl-10"
                />
              </div>
            </Field>

            {error && (
              <div className="flex items-center gap-2 p-3 rounded-lg border border-destructive/40 bg-destructive/10">
                <AlertCircle size={14} className="text-destructive shrink-0" />
                <p className="text-xs text-destructive">{error}</p>
              </div>
            )}

            <Button
              type="submit"
              className="w-full"
              disabled={loading || !key.trim()}
              loading={loading}
            >
              {loading ? "Verifying..." : "Continue"}
            </Button>
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
