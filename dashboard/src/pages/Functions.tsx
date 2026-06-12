import { useNavigate } from "react-router-dom";
import { useState, useEffect, useCallback } from "react";
import { Code2, Package, Trash2, CheckCircle, AlertCircle } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Button, Field, Input, ListRow, Panel, Section } from "../components/ui";
import { useBackend } from "../console/BackendContext";

interface DepsState {
  dependencies: Record<string, string>;
  has_lock: boolean;
  readonly: boolean;
}

export function Functions() {
  const backend = useBackend();
  const { config } = useConfig();
  const navigate = useNavigate();

  const [deps, setDeps] = useState<DepsState | null>(null);
  const [depsLoading, setDepsLoading] = useState(false);
  const [addPkg, setAddPkg] = useState("");
  const [installing, setInstalling] = useState(false);
  const [installError, setInstallError] = useState<string | null>(null);
  const [installSuccess, setInstallSuccess] = useState<string | null>(null);
  useEffect(() => {
    setDepsLoading(true);
    backend.getFunctionDeps()
      .then(setDeps)
      .catch(() => setDeps(null)) // 501 or auth error → feature unavailable
      .finally(() => setDepsLoading(false));
  }, [backend]);

  const runNpm = useCallback(async (add: string[], remove: string[]) => {
    setInstalling(true);
    setInstallError(null);
    setInstallSuccess(null);
    try {
      const updated = await backend.postFunctionDeps(add, remove);
      setDeps(updated);
      if (add.length > 0) {
        setInstallSuccess(`Installed ${add.join(", ")}`);
        setTimeout(() => setInstallSuccess(null), 4000);
      }
      if (remove.length > 0) {
        setInstallSuccess(`Removed ${remove.join(", ")}`);
        setTimeout(() => setInstallSuccess(null), 4000);
      }
    } catch (e: any) {
      const detail = e.body?.detail || e.message || "npm failed";
      setInstallError(detail);
    } finally {
      setInstalling(false);
    }
  }, [backend]);

  const handleAdd = useCallback(() => {
    const pkg = addPkg.trim();
    if (!pkg || installing) return;
    setAddPkg("");
    runNpm([pkg], []);
  }, [addPkg, installing, runNpm]);

  const handleRemove = useCallback(
    (pkg: string) => {
      if (installing) return;
      runNpm([], [pkg]);
    },
    [installing, runNpm]
  );

  if (!config) return null;

  const functions = Object.entries(config.functions || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  const depEntries = deps ? Object.entries(deps.dependencies).sort(([a], [b]) => a.localeCompare(b)) : [];
  const canEdit = backend.capabilities.canManageDeps && deps !== null && !deps.readonly;

  return (
    <div>
      <div className="pb-8 space-y-6 max-w-3xl">
        <p className="text-sm text-muted-foreground">
          {functions.length} code function{functions.length !== 1 ? "s" : ""}
        </p>
        {functions.length === 0 ? (
          <EmptyState
            icon={Code2}
            title="No code functions"
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

        {/* Dependencies panel — only shown when the endpoint is reachable */}
        {!depsLoading && deps !== null && (
          <Section
            title="Dependencies"
            icon={Package}
            actions={
              deps.has_lock ? (
                <StatusBadge variant="success">lock file</StatusBadge>
              ) : depEntries.length > 0 ? (
                <StatusBadge variant="warning">no lock file</StatusBadge>
              ) : null
            }
          >
            {depEntries.length > 0 ? (
              <div className="space-y-1.5">
                {depEntries.map(([pkg, ver]) => (
                  <Panel key={pkg} className="flex items-center gap-3 px-3 py-2">
                    <span className="text-sm font-mono text-foreground flex-1 truncate">
                      {pkg}
                    </span>
                    <span className="text-xs font-mono text-muted-foreground shrink-0">
                      {ver as string}
                    </span>
                    {canEdit && (
                      <Button
                        variant="danger-ghost"
                        size="icon"
                        aria-label={`Remove ${pkg}`}
                        disabled={installing}
                        onClick={() => handleRemove(pkg)}
                      >
                        <Trash2 size={13} />
                      </Button>
                    )}
                  </Panel>
                ))}
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">
                No dependencies installed.
              </p>
            )}

            {canEdit && (
              <Field label="Add package">
                <div className="flex gap-2">
                  <Input
                    mono
                    className="flex-1"
                    placeholder="e.g. axios or axios@latest"
                    value={addPkg}
                    onChange={(e) => setAddPkg(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") handleAdd();
                    }}
                    disabled={installing}
                  />
                  <Button
                    onClick={handleAdd}
                    disabled={installing || !addPkg.trim()}
                  >
                    {installing ? "Installing…" : "Install"}
                  </Button>
                </div>
              </Field>
            )}

            {installSuccess && (
              <div className="flex items-center gap-2 text-sm text-success">
                <CheckCircle size={14} />
                {installSuccess}
              </div>
            )}
            {installError && (
              <div className="space-y-1">
                <div className="flex items-center gap-2 text-sm text-destructive">
                  <AlertCircle size={14} />
                  npm failed
                </div>
                <pre className="text-xs text-muted-foreground bg-surface rounded p-2 overflow-x-auto whitespace-pre-wrap">
                  {installError}
                </pre>
              </div>
            )}
          </Section>
        )}
      </div>
    </div>
  );
}
