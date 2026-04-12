import { useNavigate } from "react-router-dom";
import { Plus, Code2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";

export function Functions() {
  const { config, save } = useConfig();
  const navigate = useNavigate();
  const dialog = useDialog();

  if (!config) return null;

  const functions = Object.entries(config.functions || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  async function addFunction() {
    const name = await dialog.prompt("Function name:");
    if (!name?.trim()) return;
    const fnName = name.trim().toLowerCase().replace(/\s+/g, "_");

    const updated = {
      ...config!,
      functions: {
        ...config!.functions,
        [fnName]: {
          description: "",
          method: "GET",
          query: "SELECT 1;",
          params: {},
          returns: { type: "rows", schema: {} },
          auth_required: true,
        },
      },
    };

    const ok = await save(updated);
    if (ok) navigate(`/functions/${fnName}`);
  }

  const METHOD_COLORS: Record<string, string> = {
    GET: "info",
    POST: "success",
    PUT: "warning",
    DELETE: "error",
  };

  return (
    <div>
      <PageHeader
        title="Functions"
        description={`${functions.length} custom SQL function${functions.length !== 1 ? "s" : ""}`}
        actions={
          <button
            onClick={addFunction}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
          >
            <Plus size={14} />
            Add Function
          </button>
        }
      />

      <div className="px-8">
        {functions.length === 0 ? (
          <EmptyState
            icon={Code2}
            title="No functions yet"
            description="Create custom SQL functions exposed as REST endpoints."
            action={
              <button
                onClick={addFunction}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors cursor-pointer"
              >
                <Plus size={14} />
                Add Function
              </button>
            }
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
                  {fn.description && (
                    <span className="text-xs text-muted-foreground">
                      {fn.description}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <StatusBadge
                    variant={
                      (METHOD_COLORS[fn.method] as any) || "muted"
                    }
                  >
                    {fn.method}
                  </StatusBadge>
                  {fn.auth_required && (
                    <StatusBadge variant="info">auth</StatusBadge>
                  )}
                  <StatusBadge variant="muted">
                    {Object.keys(fn.params || {}).length} param
                    {Object.keys(fn.params || {}).length !== 1 ? "s" : ""}
                  </StatusBadge>
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
