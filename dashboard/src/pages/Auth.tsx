import { useState, useEffect } from "react";
import { Shield, Plus, Trash2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { EmptyState } from "../components/EmptyState";
import type { Auth, Field } from "../lib/types";
import { POSTGRES_TYPES } from "../lib/utils";

function GoogleIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none">
      <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4" />
      <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853" />
      <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18A10.96 10.96 0 0 0 1 12c0 1.77.42 3.45 1.18 4.93l3.66-2.84z" fill="#FBBC05" />
      <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335" />
    </svg>
  );
}

function GitHubIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor">
      <path d="M12 1.27a11 11 0 0 0-3.48 21.46c.55.09.73-.28.73-.55v-1.84c-3.03.64-3.67-1.46-3.67-1.46-.5-1.29-1.21-1.64-1.21-1.64-.99-.68.07-.66.07-.66 1.09.07 1.67 1.13 1.67 1.13.97 1.67 2.56 1.19 3.18.91.1-.7.38-1.19.69-1.46-2.42-.27-4.97-1.21-4.97-5.39 0-1.19.42-2.17 1.12-2.93-.11-.28-.49-1.39.11-2.89 0 0 .92-.3 3 1.12a10.44 10.44 0 0 1 5.5 0c2.08-1.42 3-1.12 3-1.12.6 1.5.22 2.61.11 2.89.7.76 1.12 1.74 1.12 2.93 0 4.19-2.56 5.11-4.98 5.38.39.34.74 1.01.74 2.04v3.02c0 .27.18.65.74.55A11 11 0 0 0 12 1.27z" />
    </svg>
  );
}

const DEFAULT_AUTH: Auth = {
  jwt_expiry: "15m",
  refresh_tokens: true,
  refresh_token_expiry: "7d",
  fields: {},
  email: { verify_email: false, templates: {} },
  google: null,
  github: null,
};

export function AuthPage() {
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [auth, setAuth] = useState<Auth | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (config) {
      setAuth(config.auth ? structuredClone(config.auth) : null);
      setEnabled(!!config.auth);
      setDirty(false);
    }
  }, [config]);

  function updateAuth(updater: (prev: Auth) => Auth) {
    setAuth((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  async function handleSave() {
    if (!config) return;
    const updated = { ...config, auth: enabled ? auth : null };
    await save(updated);
    setDirty(false);
  }

  function toggleAuth() {
    setEnabled((prev) => {
      const next = !prev;
      if (next && !auth) setAuth(structuredClone(DEFAULT_AUTH));
      setDirty(true);
      return next;
    });
  }

  if (!config) return null;

  return (
    <div className="pb-20">
      <PageHeader
        title="Authentication"
        description="Configure auth providers, JWT settings, and custom user fields"
      />

      <div className="px-8 space-y-6">
        {/* Enable/Disable Toggle */}
        <div className="flex items-center justify-between p-4 rounded-xl border border-border bg-surface">
          <div>
            <p className="text-sm font-medium text-foreground">Authentication</p>
            <p className="text-xs text-muted-foreground mt-0.5">
              Enable email/password and OAuth authentication
            </p>
          </div>
          <button
            onClick={toggleAuth}
            className={`relative w-11 h-6 rounded-full transition-colors cursor-pointer ${
              enabled ? "bg-accent" : "bg-secondary"
            }`}
          >
            <span
              className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white transition-transform ${
                enabled ? "translate-x-5" : ""
              }`}
            />
          </button>
        </div>

        {!enabled ? (
          <EmptyState
            icon={Shield}
            title="Auth is disabled"
            description="Enable authentication to configure providers and JWT settings."
          />
        ) : auth ? (
          <>
            {/* JWT Settings */}
            <section className="space-y-4">
              <h2 className="text-sm font-medium text-foreground">JWT Settings</h2>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-xs font-medium text-muted-foreground mb-1">JWT Expiry</label>
                  <input
                    type="text"
                    value={auth.jwt_expiry}
                    onChange={(e) => updateAuth((a) => ({ ...a, jwt_expiry: e.target.value }))}
                    placeholder="15m"
                    className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-muted-foreground mb-1">Refresh Token Expiry</label>
                  <input
                    type="text"
                    value={auth.refresh_token_expiry}
                    onChange={(e) => updateAuth((a) => ({ ...a, refresh_token_expiry: e.target.value }))}
                    placeholder="7d"
                    className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
                  />
                </div>
              </div>
              <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                <input
                  type="checkbox"
                  checked={auth.refresh_tokens}
                  onChange={(e) => updateAuth((a) => ({ ...a, refresh_tokens: e.target.checked }))}
                  className="rounded border-border"
                />
                Enable refresh tokens
              </label>
            </section>

            {/* Custom Fields */}
            <section className="space-y-4">
              <div className="flex items-center justify-between">
                <h2 className="text-sm font-medium text-foreground">Custom User Fields</h2>
                <button
                  onClick={async () => {
                    const name = await dialog.prompt("Field name:");
                    if (!name?.trim()) return;
                    updateAuth((a) => ({
                      ...a,
                      fields: { ...a.fields, [name.trim()]: { type: "text" } },
                    }));
                  }}
                  className="inline-flex items-center gap-1 px-2 py-1 rounded border border-dashed border-border text-xs text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
                >
                  <Plus size={12} />
                  Add Field
                </button>
              </div>

              {/* System fields (read-only) */}
              <div className="space-y-1">
                {["id", "email", "password_hash", "email_verified", "created_at"].map((f) => (
                  <div
                    key={f}
                    className="flex items-center gap-3 px-3 py-2 rounded-lg bg-muted/50"
                  >
                    <span className="text-xs font-mono text-muted-foreground">{f}</span>
                    <span className="text-xs text-muted-foreground/60">system field</span>
                  </div>
                ))}
              </div>

              {/* Custom fields */}
              {Object.entries(auth.fields || {}).map(([fieldName, field]) => (
                <div
                  key={fieldName}
                  className="flex items-center gap-3 px-3 py-2 rounded-lg border border-border bg-primary"
                >
                  <span className="text-sm font-mono text-foreground min-w-[120px]">{fieldName}</span>
                  <select
                    value={field.type}
                    onChange={(e) =>
                      updateAuth((a) => ({
                        ...a,
                        fields: { ...a.fields, [fieldName]: { ...field, type: e.target.value } },
                      }))
                    }
                    className="px-2 py-1 rounded border border-border bg-input text-xs font-mono text-foreground cursor-pointer focus:outline-none focus:border-ring"
                  >
                    {POSTGRES_TYPES.map((t) => (
                      <option key={t} value={t}>{t}</option>
                    ))}
                  </select>
                  <label className="flex items-center gap-1 text-xs text-foreground cursor-pointer">
                    <input
                      type="checkbox"
                      checked={field.required || false}
                      onChange={(e) =>
                        updateAuth((a) => ({
                          ...a,
                          fields: { ...a.fields, [fieldName]: { ...field, required: e.target.checked } },
                        }))
                      }
                      className="rounded border-border"
                    />
                    Required
                  </label>
                  <button
                    onClick={() =>
                      updateAuth((a) => {
                        const { [fieldName]: _, ...rest } = a.fields;
                        return { ...a, fields: rest };
                      })
                    }
                    className="ml-auto p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              ))}
            </section>

            {/* Email Verification */}
            <section className="space-y-4">
              <h2 className="text-sm font-medium text-foreground">Email Verification</h2>
              <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                <input
                  type="checkbox"
                  checked={auth.email?.verify_email || false}
                  onChange={(e) =>
                    updateAuth((a) => ({
                      ...a,
                      email: {
                        ...(a.email || { verify_email: false, templates: {} }),
                        verify_email: e.target.checked,
                      },
                    }))
                  }
                  className="rounded border-border"
                />
                Require email verification
              </label>

              {auth.email?.verify_email && (
                <div className="space-y-4 pl-6 border-l-2 border-border">
                  {["verify", "reset"].map((templateName) => {
                    const template = auth.email?.templates?.[templateName] || {
                      subject: "",
                      body: "",
                      body_file: "",
                    };
                    return (
                      <div key={templateName} className="space-y-2">
                        <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                          {templateName} Template
                        </h3>
                        <input
                          type="text"
                          value={template.subject}
                          onChange={(e) =>
                            updateAuth((a) => ({
                              ...a,
                              email: {
                                ...a.email!,
                                templates: {
                                  ...a.email!.templates,
                                  [templateName]: { ...template, subject: e.target.value },
                                },
                              },
                            }))
                          }
                          placeholder="Subject"
                          className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
                        />
                        <CodeEditor
                          value={template.body}
                          onChange={(val) =>
                            updateAuth((a) => ({
                              ...a,
                              email: {
                                ...a.email!,
                                templates: {
                                  ...a.email!.templates,
                                  [templateName]: { ...template, body: val },
                                },
                              },
                            }))
                          }
                          language="text"
                          placeholder="Template body... Use {{link}}, {{data.display_name}}, {{project.name}}"
                          minHeight="80px"
                        />
                      </div>
                    );
                  })}
                </div>
              )}
            </section>

            {/* OAuth Providers */}
            <section className="space-y-4">
              <h2 className="text-sm font-medium text-foreground">OAuth Providers</h2>

              {(["google", "github"] as const).map((provider) => {
                const config = auth[provider];
                const isEnabled = !!config;

                return (
                  <div key={provider} className="p-4 rounded-xl border border-border bg-surface space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="flex items-center gap-2 text-sm font-medium text-foreground capitalize">
                        {provider === "google" ? <GoogleIcon size={18} /> : <GitHubIcon size={18} />}
                        {provider}
                      </span>
                      <button
                        onClick={() =>
                          updateAuth((a) => ({
                            ...a,
                            [provider]: isEnabled
                              ? null
                              : { client_id: "", client_secret: "", redirect_url: "" },
                          }))
                        }
                        className={`relative w-11 h-6 rounded-full transition-colors cursor-pointer ${
                          isEnabled ? "bg-accent" : "bg-secondary"
                        }`}
                      >
                        <span
                          className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white transition-transform ${
                            isEnabled ? "translate-x-5" : ""
                          }`}
                        />
                      </button>
                    </div>

                    {isEnabled && config && (
                      <div className="grid grid-cols-1 gap-3">
                        {(["client_id", "client_secret", "redirect_url"] as const).map((field) => (
                          <div key={field}>
                            <label className="block text-xs font-medium text-muted-foreground mb-1">
                              {field.replace(/_/g, " ")}
                            </label>
                            <input
                              type={field === "client_secret" ? "password" : "text"}
                              value={config[field]}
                              onChange={(e) =>
                                updateAuth((a) => ({
                                  ...a,
                                  [provider]: { ...a[provider]!, [field]: e.target.value },
                                }))
                              }
                              placeholder={`\${${provider.toUpperCase()}_${field.toUpperCase()}}`}
                              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring transition-colors"
                            />
                          </div>
                        ))}
                        <p className="text-xs text-muted-foreground">
                          Use <code className="font-mono text-accent">${"{ENV_VAR}"}</code> syntax for
                          env var interpolation at runtime.
                        </p>
                      </div>
                    )}
                  </div>
                );
              })}
            </section>
          </>
        ) : null}
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
