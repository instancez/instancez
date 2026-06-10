import { useNavigate } from "react-router-dom";
import { Code2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";

export function Functions() {
  const { config } = useConfig();
  const navigate = useNavigate();
  if (!config) return null;

  const functions = Object.entries(config.functions || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  return (
    <div>
      <PageHeader
        title="Edge Functions"
        description={`${functions.length} code function${functions.length !== 1 ? "s" : ""}`}
      />
      <div className="px-8">
        {functions.length === 0 ? (
          <EmptyState
            icon={Code2}
            title="No edge functions"
            description="Declare a function in instancez.yaml with a runtime and a .js file under functions/ (served at /functions/v1/<name>)."
          />
        ) : (
          <div className="space-y-2">
            {functions.map(([name, fn]) => (
              <button
                key={name}
                onClick={() => navigate(`/functions/${name}`)}
                className="w-full flex items-center justify-between px-5 py-3.5 rounded-lg border border-border bg-surface hover:bg-surface-hover hover:border-border-hover transition-colors cursor-pointer text-left group"
              >
                <div className="flex items-center gap-3">
                  <Code2
                    size={16}
                    className="text-muted-foreground group-hover:text-foreground transition-colors"
                  />
                  <span className="text-sm font-mono font-medium text-foreground">
                    {name}
                  </span>
                  <span className="text-xs font-mono text-muted-foreground">
                    {fn.file}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <StatusBadge variant="info">{fn.runtime || "node"}</StatusBadge>
                  {fn.auth_required && (
                    <StatusBadge variant="info">auth</StatusBadge>
                  )}
                  {fn.timeout && (
                    <StatusBadge variant="muted">{fn.timeout}</StatusBadge>
                  )}
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
