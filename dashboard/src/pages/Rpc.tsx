import { useNavigate } from "react-router-dom";
import { Plus, Database } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow } from "../components/ui";

export function Rpc() {
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();

  if (!config) return null;

  const functions = Object.entries(config.rpc || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  async function addFunction() {
    const name = await dialog.prompt("Function name:");
    if (!name?.trim()) return;
    const fnName = name.trim().toLowerCase().replace(/\s+/g, "_");

    const updated = {
      ...config!,
      rpc: {
        ...(config!.rpc || {}),
        [fnName]: {
          description: "",
          auth_required: true,
          language: "plpgsql",
          volatility: "volatile",
          security: "invoker",
          args: [],
          body: "BEGIN\n  -- function body\nEND;",
          returns: { type: "void" },
        },
      },
    };

    const ok = await save(updated);
    if (ok) navigate(`/rpc/${fnName}`);
  }

  const addButton = (
    <Button onClick={addFunction}>
      <Plus size={14} />
      Add Function
    </Button>
  );

  return (
    <div>
      <PageHeader
        title="Database Functions"
        description={`${functions.length} SQL function${functions.length !== 1 ? "s" : ""}`}
        actions={addButton}
      />

      <div className="px-8 pb-8">
        {functions.length === 0 ? (
          <EmptyState
            icon={Database}
            title="No SQL functions yet"
            description="Create Postgres functions exposed at /rest/v1/rpc."
            action={addButton}
          />
        ) : (
          <div className="space-y-2">
            {functions.map(([name, fn]) => (
              <ListRow
                key={name}
                icon={Database}
                title={name}
                meta={fn.description || undefined}
                onClick={() => navigate(`/rpc/${name}`)}
                badges={
                  <>
                    <StatusBadge variant="info">{fn.language}</StatusBadge>
                    {fn.auth_required && (
                      <StatusBadge variant="info">auth</StatusBadge>
                    )}
                    <StatusBadge variant="muted">
                      {(fn.args || []).length} arg
                      {(fn.args || []).length !== 1 ? "s" : ""}
                    </StatusBadge>
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
