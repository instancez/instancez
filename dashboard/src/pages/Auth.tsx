import { useState, useEffect, useCallback } from "react";
import { Shield, KeySquare, MailCheck, Plug2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { EmptyState } from "../components/EmptyState";
import { Toggle } from "../components/Toggle";
import { Field, Input, Panel, Section } from "../components/ui";
import { getEnvVars, putDotenv } from "../api/client";
import type { Auth } from "../lib/types";

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

const OAUTH_VARS: Record<string, Record<string, string>> = {
  google: {
    client_id: "INSTANCEZ_GOOGLE_CLIENT_ID",
    client_secret: "INSTANCEZ_GOOGLE_CLIENT_SECRET",
    redirect_url: "INSTANCEZ_GOOGLE_REDIRECT_URL",
  },
  github: {
    client_id: "INSTANCEZ_GITHUB_CLIENT_ID",
    client_secret: "INSTANCEZ_GITHUB_CLIENT_SECRET",
    redirect_url: "INSTANCEZ_GITHUB_REDIRECT_URL",
  },
};

const DEFAULT_AUTH: Auth = {
  jwt_expiry: "15m",
  refresh_tokens: true,
  refresh_token_expiry: "7d",
  email: { verify_email: false, templates: {} },
  google: null,
  github: null,
};

interface VarRowProps {
  name: string;
  isSet: boolean;
  canWrite: boolean;
  inputValue: string;
  onInputChange: (value: string) => void;
}

function VarRow({ name, isSet, canWrite, inputValue, onInputChange }: VarRowProps) {
  return (
    <div className="flex items-center gap-3 py-1">
      <code className="flex-1 min-w-0 text-xs font-mono text-foreground truncate">{name}</code>
      <span
        className={`shrink-0 text-xs font-medium ${isSet ? "text-green-600 dark:text-green-400" : "text-destructive"}`}
      >
        {isSet ? "✓ set" : "✗ unset"}
      </span>
      {canWrite && (
        <Input
          type="password"
          placeholder="enter value…"
          value={inputValue}
          onChange={(e) => onInputChange(e.target.value)}
          className="w-48 h-7 text-xs"
        />
      )}
    </div>
  );
}

export function AuthPage() {
  const { config, save, saving, saveErrors, dotenvWritable } = useConfig();
  const [auth, setAuth] = useState<Auth | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [envVarStatus, setEnvVarStatus] = useState<Record<string, { set: boolean }>>({});
  const [pendingDotenv, setPendingDotenv] = useState<Record<string, string>>({});

  const loadEnvVars = useCallback(async () => {
    try {
      const resp = await getEnvVars();
      setEnvVarStatus(resp.vars);
    } catch {
      // badges fall back to "✗ unset" when status unavailable
    }
  }, []);

  useEffect(() => {
    if (config) {
      setAuth(config.auth ? structuredClone(config.auth) : null);
      setEnabled(!!config.auth);
      setDirty(false);
    }
  }, [config]);

  useEffect(() => {
    loadEnvVars();
  }, [loadEnvVars]);

  function updateAuth(updater: (prev: Auth) => Auth) {
    setAuth((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  function setPendingVar(name: string, value: string) {
    setPendingDotenv((prev) => ({ ...prev, [name]: value }));
    setDirty(true);
  }

  async function handleSave() {
    if (!config) return;
    const updated = { ...config, auth: enabled ? auth : null };
    const ok = await save(updated);
    if (ok) {
      const toWrite = Object.fromEntries(
        Object.entries(pendingDotenv).filter(([, v]) => v !== "")
      );
      if (dotenvWritable && Object.keys(toWrite).length > 0) {
        await putDotenv(toWrite).catch(() => {});
      }
      setPendingDotenv({});
      await loadEnvVars();
      setDirty(false);
    }
  }

  function toggleAuth() {
    setEnabled((prev) => {
      const next = !prev;
      if (next && !auth) setAuth(structuredClone(DEFAULT_AUTH));
      setDirty(true);
      return next;
    });
  }

  function toggleOAuth(provider: "google" | "github", isEnabled: boolean) {
    if (isEnabled) {
      const vars = OAUTH_VARS[provider];
      setPendingDotenv((prev) => {
        const next = { ...prev };
        Object.values(vars).forEach((v) => delete next[v]);
        return next;
      });
      updateAuth((a) => ({ ...a, [provider]: null }));
    } else {
      const vars = OAUTH_VARS[provider];
      updateAuth((a) => ({
        ...a,
        [provider]: {
          client_id: `\${${vars.client_id}}`,
          client_secret: `\${${vars.client_secret}}`,
          redirect_url: `\${${vars.redirect_url}}`,
        },
      }));
    }
  }

  if (!config) return null;

  return (
    <div className="pb-20">
      <PageHeader
        title="Authentication"
        description="Configure auth providers and JWT settings"
      />

      <div className="px-8 pb-8 space-y-6 max-w-3xl">
        {/* Enable/Disable Toggle */}
        <Section
          title="Enable Authentication"
          description="Email/password and OAuth authentication"
          icon={Shield}
          actions={
            <Toggle
              aria-label="Enable authentication"
              checked={enabled}
              onChange={toggleAuth}
            />
          }
        />

        {!enabled ? (
          <EmptyState
            icon={Shield}
            title="Auth is disabled"
            description="Enable authentication to configure providers and JWT settings."
          />
        ) : auth ? (
          <>
            <Section
              title="JWT Settings"
              description="Token lifetimes for issued sessions"
              icon={KeySquare}
            >
              <div className="grid grid-cols-2 gap-4">
                <Field label="JWT Expiry">
                  <Input
                    mono
                    value={auth.jwt_expiry}
                    onChange={(e) => updateAuth((a) => ({ ...a, jwt_expiry: e.target.value }))}
                    placeholder="15m"
                  />
                </Field>
                <Field label="Refresh Token Expiry">
                  <Input
                    mono
                    value={auth.refresh_token_expiry}
                    onChange={(e) => updateAuth((a) => ({ ...a, refresh_token_expiry: e.target.value }))}
                    placeholder="7d"
                  />
                </Field>
              </div>
              <Toggle
                checked={auth.refresh_tokens}
                onChange={(v) => updateAuth((a) => ({ ...a, refresh_tokens: v }))}
                label="Enable refresh tokens"
              />
            </Section>

            <Section
              title="Email Verification"
              description="Require users to confirm their address before signing in"
              icon={MailCheck}
            >
              <Toggle
                checked={auth.email?.verify_email || false}
                onChange={(v) =>
                  updateAuth((a) => ({
                    ...a,
                    email: {
                      ...(a.email || { verify_email: false, templates: {} }),
                      verify_email: v,
                    },
                  }))
                }
                label="Require email verification"
              />

              {auth.email?.verify_email && (
                <div className="space-y-4">
                  {["verify", "reset"].map((templateName) => {
                    const template = auth.email?.templates?.[templateName] || {
                      subject: "",
                      body: "",
                      body_file: "",
                    };
                    return (
                      <Panel key={templateName} className="p-4 space-y-3">
                        <Field label={`${templateName} template`}>
                          <Input
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
                          />
                        </Field>
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
                      </Panel>
                    );
                  })}
                </div>
              )}
            </Section>

            <Section
              title="OAuth Providers"
              description="Third-party sign-in via OAuth 2.0"
              icon={Plug2}
            >
              {(["google", "github"] as const).map((provider) => {
                const providerConfig = auth[provider];
                const isEnabled = !!providerConfig;
                const vars = OAUTH_VARS[provider];

                return (
                  <Panel key={provider} className="p-4 space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="flex items-center gap-2 text-sm font-medium text-foreground capitalize">
                        {provider === "google" ? <GoogleIcon size={18} /> : <GitHubIcon size={18} />}
                        {provider}
                      </span>
                      <Toggle
                        aria-label={`Enable ${provider}`}
                        checked={isEnabled}
                        onChange={() => toggleOAuth(provider, isEnabled)}
                      />
                    </div>

                    {isEnabled && (
                      <div className="space-y-1">
                        {(["client_id", "client_secret", "redirect_url"] as const).map((field) => {
                          const varName = vars[field];
                          return (
                            <VarRow
                              key={field}
                              name={varName}
                              isSet={envVarStatus[varName]?.set ?? false}
                              canWrite={dotenvWritable}
                              inputValue={pendingDotenv[varName] ?? ""}
                              onInputChange={(v) => setPendingVar(varName, v)}
                            />
                          );
                        })}
                      </div>
                    )}
                  </Panel>
                );
              })}
            </Section>
          </>
        ) : null}
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
