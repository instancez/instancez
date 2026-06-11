import { useNavigate } from "react-router-dom";
import { Code2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { ListRow } from "../components/ui";

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
      <div className="px-8 pb-8">
        {functions.length === 0 ? (
          <EmptyState
            icon={Code2}
            title="No edge functions"
            description="Declare a function in instancez.yaml with a runtime and a .js file under functions/ (served at /functions/v1/<name>)."
          />
        ) : (
          <div className="space-y-2">
            {functions.map(([name, fn]) => (
              <ListRow
                key={name}
                icon={Code2}
                title={name}
                meta={<span className="font-mono">{fn.file}</span>}
                onClick={() => navigate(`/functions/${name}`)}
                badges={
                  <>
                    <StatusBadge variant="info">{fn.runtime || "node"}</StatusBadge>
                    {fn.auth_required && (
                      <StatusBadge variant="info">auth</StatusBadge>
                    )}
                    {fn.timeout && (
                      <StatusBadge variant="muted">{fn.timeout}</StatusBadge>
                    )}
                  </>
                }
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
