import { useState, useEffect } from "react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { TagInput } from "../components/TagInput";
import { CORS_METHODS } from "../lib/utils";
import type { Config } from "../lib/types";

export function SettingsPage() {
  const { config, save, saving, saveErrors } = useConfig();
  const [local, setLocal] = useState<Config | null>(null);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (config) {
      setLocal(structuredClone(config));
      setDirty(false);
    }
  }, [config]);

  function update(updater: (prev: Config) => Config) {
    setLocal((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  async function handleSave() {
    if (!local) return;
    await save(local);
    setDirty(false);
  }

  if (!local) return null;

  return (
    <div className="pb-20">
      <PageHeader
        title="Server Settings"
        description="Configure project metadata, server, CORS, timeouts, and database pool"
      />

      <div className="px-8 space-y-8 max-w-2xl">
        {/* Project */}
        <section className="space-y-4">
          <h2 className="text-sm font-semibold text-foreground">Project</h2>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Name</label>
              <input
                type="text"
                value={local.project.name}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    project: { ...c.project, name: e.target.value },
                  }))
                }
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Description</label>
              <input
                type="text"
                value={local.project.description}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    project: { ...c.project, description: e.target.value },
                  }))
                }
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground focus:outline-none focus:border-ring transition-colors"
              />
            </div>
          </div>
        </section>

        {/* Extensions */}
        <section className="space-y-3">
          <h2 className="text-sm font-semibold text-foreground">Postgres Extensions</h2>
          <TagInput
            value={local.extensions || []}
            onChange={(exts) => update((c) => ({ ...c, extensions: exts }))}
            placeholder="e.g. pgcrypto, pg_trgm"
            suggestions={[
              "pgcrypto",
              "pg_trgm",
              "uuid-ossp",
              "hstore",
              "postgis",
              "citext",
              "tablefunc",
              "btree_gin",
              "btree_gist",
            ]}
          />
        </section>

        {/* Server */}
        <section className="space-y-4">
          <h2 className="text-sm font-semibold text-foreground">Server</h2>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Port</label>
              <input
                type="number"
                value={local.server.port}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: { ...c.server, port: parseInt(e.target.value) || 8080 },
                  }))
                }
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground tabular-nums focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Max Body Size</label>
              <input
                type="text"
                value={local.server.max_body_size}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: { ...c.server, max_body_size: e.target.value },
                  }))
                }
                placeholder="10MB"
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Max Limit</label>
              <input
                type="number"
                value={local.server.max_limit}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: { ...c.server, max_limit: parseInt(e.target.value) || 1000 },
                  }))
                }
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground tabular-nums focus:outline-none focus:border-ring transition-colors"
              />
            </div>
          </div>
          <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
            <input
              type="checkbox"
              checked={local.server.docs_ui ?? true}
              onChange={(e) =>
                update((c) => ({
                  ...c,
                  server: { ...c.server, docs_ui: e.target.checked },
                }))
              }
              className="rounded border-border"
            />
            Enable API docs UI
          </label>
        </section>

        {/* CORS */}
        <section className="space-y-4">
          <h2 className="text-sm font-semibold text-foreground">CORS</h2>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Origins</label>
            <TagInput
              value={local.server.cors.origins || []}
              onChange={(origins) =>
                update((c) => ({
                  ...c,
                  server: { ...c.server, cors: { ...c.server.cors, origins } },
                }))
              }
              placeholder="e.g. http://localhost:3000"
              suggestions={["*", "http://localhost:3000", "http://localhost:5173"]}
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Methods</label>
            <TagInput
              value={local.server.cors.methods || []}
              onChange={(methods) =>
                update((c) => ({
                  ...c,
                  server: { ...c.server, cors: { ...c.server.cors, methods } },
                }))
              }
              placeholder="e.g. GET, POST, DELETE"
              suggestions={[...CORS_METHODS]}
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Headers</label>
            <TagInput
              value={local.server.cors.headers || []}
              onChange={(headers) =>
                update((c) => ({
                  ...c,
                  server: { ...c.server, cors: { ...c.server.cors, headers } },
                }))
              }
              placeholder="e.g. Authorization, Content-Type"
              suggestions={["Authorization", "Content-Type", "X-Requested-With"]}
            />
          </div>
          <div className="flex items-center gap-6">
            <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
              <input
                type="checkbox"
                checked={local.server.cors.credentials}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: {
                      ...c.server,
                      cors: { ...c.server.cors, credentials: e.target.checked },
                    },
                  }))
                }
                className="rounded border-border"
              />
              Credentials
            </label>
            <div>
              <label className="text-xs font-medium text-muted-foreground mr-2">Max Age (s)</label>
              <input
                type="number"
                value={local.server.cors.max_age}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: {
                      ...c.server,
                      cors: { ...c.server.cors, max_age: parseInt(e.target.value) || 0 },
                    },
                  }))
                }
                className="w-24 px-2 py-1 rounded-lg border border-border bg-input text-sm text-foreground tabular-nums focus:outline-none focus:border-ring transition-colors"
              />
            </div>
          </div>
        </section>

        {/* Timeouts */}
        <section className="space-y-4">
          <h2 className="text-sm font-semibold text-foreground">Timeouts</h2>
          <div className="grid grid-cols-2 gap-4">
            {(
              [
                ["request", "Request"],
                ["db_query", "DB Query"],
                ["upload", "Upload"],
                ["shutdown", "Shutdown"],
              ] as const
            ).map(([key, label]) => (
              <div key={key}>
                <label className="block text-xs font-medium text-muted-foreground mb-1">{label}</label>
                <input
                  type="text"
                  value={(local.server.timeouts as any)[key] || ""}
                  onChange={(e) =>
                    update((c) => ({
                      ...c,
                      server: {
                        ...c.server,
                        timeouts: { ...c.server.timeouts, [key]: e.target.value },
                      },
                    }))
                  }
                  placeholder="30s"
                  className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
                />
              </div>
            ))}
          </div>
        </section>

        {/* Database Pool */}
        <section className="space-y-4">
          <h2 className="text-sm font-semibold text-foreground">Database Pool</h2>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Max Connections</label>
              <input
                type="number"
                value={local.server.db?.pool?.max || 0}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: {
                      ...c.server,
                      db: {
                        ...c.server.db,
                        pool: { ...c.server.db.pool, max: parseInt(e.target.value) || 0 },
                      },
                    },
                  }))
                }
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground tabular-nums focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Min Connections</label>
              <input
                type="number"
                value={local.server.db?.pool?.min || 0}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: {
                      ...c.server,
                      db: {
                        ...c.server.db,
                        pool: { ...c.server.db.pool, min: parseInt(e.target.value) || 0 },
                      },
                    },
                  }))
                }
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground tabular-nums focus:outline-none focus:border-ring transition-colors"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1">Idle Timeout</label>
              <input
                type="text"
                value={local.server.db?.pool?.idle_timeout || ""}
                onChange={(e) =>
                  update((c) => ({
                    ...c,
                    server: {
                      ...c.server,
                      db: {
                        ...c.server.db,
                        pool: { ...c.server.db.pool, idle_timeout: e.target.value },
                      },
                    },
                  }))
                }
                placeholder="5m"
                className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
              />
            </div>
          </div>
        </section>


      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
