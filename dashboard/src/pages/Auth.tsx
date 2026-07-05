import { useState, useEffect, useCallback } from "react";
import { Shield, KeySquare, Mails, Plug2, UserPlus, Link2, Plus, X } from "lucide-react";
import { Box, Grid, HStack, IconButton, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { SaveBar } from "../components/SaveBar";
import { CodeEditor } from "../components/CodeEditor";
import { EmptyState } from "../components/EmptyState";
import { Toggle } from "../components/Toggle";
import { Field, Input, Panel, Section } from "../components/ui";
import { VarRow } from "../components/VarRow";
import { useBackend } from "../console/BackendContext";
import { envRefName } from "../lib/envRef";
import { isValidRedirectOrigin } from "../lib/redirectOrigin";
import { jsonEqual } from "../lib/jsonEqual";
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

// OAuth provider configs: the client secret is a credential (always a ${VAR}
// ref in YAML, value in .env, rendered first); client ID and redirect URL are
// ordinary settings stored as literal YAML values (a hand-edited ${VAR} ref
// is respected and rendered as env-managed).
const OAUTH_SECRET_VARS: Record<"google" | "github", string> = {
  google: "INSTANCEZ_GOOGLE_CLIENT_SECRET",
  github: "INSTANCEZ_GITHUB_CLIENT_SECRET",
};

const OAUTH_SETTINGS = [
  { key: "client_id", label: "Client ID" },
  { key: "redirect_url", label: "Redirect URL", placeholder: "https://example.com/auth/callback" },
] as const;

// Credential vars are probed for set/unset status even before their ${VAR}
// refs are saved into the YAML (the backend only scans the YAML on its own).
const OAUTH_CRED_VARS = Object.values(OAUTH_SECRET_VARS);

// collectOAuthRefs finds ${VAR} refs in saved OAuth settings so their status
// badges resolve too.
function collectOAuthRefs(auth: Auth | null): string[] {
  if (!auth) return [];
  const names: string[] = [];
  for (const provider of Object.values(auth.oauth ?? {})) {
    if (!provider) continue;
    for (const value of Object.values(provider)) {
      const name = envRefName(value);
      if (name) names.push(name);
    }
  }
  return names;
}

// Mirrors defaultEmailTemplates in internal/adapter/http/auth_email_defaults.go —
// shown as placeholders so users see exactly what is sent without an override.
const EMAIL_TEMPLATES = [
  {
    key: "verification",
    label: "Verification",
    vars: "{{link}}, {{token}}, {{email}}, {{base_url}}",
    defaultSubject: "Confirm your email",
    defaultBody: `Hi,

Thanks for signing up. Confirm your email address by clicking the link below:

{{link}}

If you didn't create an account, you can safely ignore this email.`,
  },
  {
    key: "magiclink",
    label: "Magic link",
    vars: "{{link}}, {{code}}, {{token}}, {{email}}, {{base_url}}",
    defaultSubject: "Your sign-in link",
    defaultBody: `Hi,

Click the link below to sign in:

{{link}}

Or enter this one-time code: {{code}}

If you didn't request this, you can safely ignore this email.`,
  },
  {
    key: "reset",
    label: "Password reset",
    vars: "{{link}}, {{token}}, {{email}}, {{base_url}}",
    defaultSubject: "Reset your password",
    defaultBody: `Hi,

We received a request to reset the password for {{email}}.

Reset it by clicking the link below:

{{link}}

If you didn't request a reset, you can safely ignore this email — your password is unchanged.`,
  },
] as const;

const DEFAULT_AUTH: Auth = {
  jwt_expiry: "15m",
  refresh_tokens: true,
  refresh_token_expiry: "7d",
  allow_signup: null,
  allow_anonymous: null,
  redirect_urls: [],
  email: { verify_email: false, templates: {} },
  oauth: {},
};

export function AuthPage() {
  const backend = useBackend();
  const { config, save, saving, saveErrors, dotenvWritable } = useConfig();
  const canWriteConfig = backend.capabilities.canWriteConfig;
  const canWriteSecrets = backend.capabilities.canWriteSecrets && dotenvWritable;
  const showEnvName = backend.capabilities.showsEnvVarNames;
  const [auth, setAuth] = useState<Auth | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [envVarStatus, setEnvVarStatus] = useState<Record<string, { set: boolean }>>({});
  const [pendingDotenv, setPendingDotenv] = useState<Record<string, string>>({});

  const loadEnvVars = useCallback(async () => {
    try {
      const resp = await backend.getEnvVars([
        ...OAUTH_CRED_VARS,
        ...collectOAuthRefs(config?.auth ?? null),
      ]);
      setEnvVarStatus(resp.vars);
    } catch {
      // badges fall back to "✗ unset" when status unavailable
    }
  }, [backend, config]);

  useEffect(() => {
    if (config) {
      setAuth(config.auth ? structuredClone(config.auth) : null);
      setEnabled(!!config.auth);
    }
  }, [config]);

  useEffect(() => {
    loadEnvVars();
  }, [loadEnvVars]);

  function updateAuth(updater: (prev: Auth) => Auth) {
    setAuth((prev) => (prev ? updater(prev) : prev));
  }

  function setPendingVar(name: string, value: string) {
    setPendingDotenv((prev) => ({ ...prev, [name]: value }));
  }

  async function handleSave() {
    if (!config) return;
    // Drop blank redirect rows (an added-but-unfilled entry) so they don't land
    // in the YAML as empty list items.
    const cleanedAuth =
      enabled && auth
        ? { ...auth, redirect_urls: (auth.redirect_urls ?? []).map((u) => u.trim()).filter(Boolean) }
        : null;
    const updated = { ...config, auth: cleanedAuth };
    const staged = Object.entries(pendingDotenv).filter(([, v]) => v !== "");
    const ok = await save(updated, {
      dotenvChanges: staged.map(([name, value]) => ({
        name,
        tail: value.slice(-4),
        isUpdate: envVarStatus[name]?.set ?? false,
      })),
    });
    if (ok) {
      if (canWriteSecrets && staged.length > 0) {
        await backend.writeSecrets(Object.fromEntries(staged)).catch(() => {});
      }
      setPendingDotenv({});
      await loadEnvVars();
    }
  }

  function toggleAuth() {
    setEnabled((prev) => {
      const next = !prev;
      if (next && !auth) setAuth(structuredClone(DEFAULT_AUTH));
      return next;
    });
  }

  function toggleOAuth(provider: "google" | "github", isEnabled: boolean) {
    if (isEnabled) {
      const secretVar = OAUTH_SECRET_VARS[provider];
      setPendingDotenv((prev) => {
        const next = { ...prev };
        delete next[secretVar];
        return next;
      });
      updateAuth((a) => {
        const oauth = { ...a.oauth };
        delete oauth[provider];
        return { ...a, oauth };
      });
    } else {
      updateAuth((a) => ({
        ...a,
        oauth: {
          ...a.oauth,
          [provider]: {
            client_id: "",
            client_secret: `\${${OAUTH_SECRET_VARS[provider]}}`,
            redirect_url: "",
          },
        },
      }));
    }
  }

  function updateOAuthSetting(
    provider: "google" | "github",
    key: "client_id" | "redirect_url",
    value: string
  ) {
    updateAuth((a) => {
      const current = a.oauth?.[provider];
      if (!current) return a;
      return { ...a, oauth: { ...a.oauth, [provider]: { ...current, [key]: value } } };
    });
  }

  function setRedirectUrl(index: number, value: string) {
    updateAuth((a) => {
      const urls = [...(a.redirect_urls ?? [])];
      urls[index] = value;
      return { ...a, redirect_urls: urls };
    });
  }

  function addRedirectUrl() {
    updateAuth((a) => ({ ...a, redirect_urls: [...(a.redirect_urls ?? []), ""] }));
  }

  function removeRedirectUrl(index: number) {
    updateAuth((a) => ({
      ...a,
      redirect_urls: (a.redirect_urls ?? []).filter((_, i) => i !== index),
    }));
  }

  if (!config) return null;

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty =
    !jsonEqual(enabled ? auth : null, config.auth ?? null) ||
    Object.values(pendingDotenv).some((v) => v !== "");

  return (
    <Box pb="20">
      <VStack pb="8" gap="6" maxW="3xl" align="stretch">
        {/* Enable/Disable Toggle */}
        <Section
          title="Enable Authentication"
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
              icon={KeySquare}
            >
              <Grid gridTemplateColumns="repeat(2, 1fr)" gap="4">
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
              </Grid>
              <Toggle
                checked={auth.refresh_tokens}
                onChange={(v) => updateAuth((a) => ({ ...a, refresh_tokens: v }))}
                label="Enable refresh tokens"
              />
            </Section>

            <Section title="Sign-up & verification" icon={UserPlus}>
              <VStack gap="4" align="stretch">
                <HStack justify="space-between" gap="3">
                  <VStack gap="0.5" align="start">
                    <Text fontSize="sm" color="fg">Allow public sign-up</Text>
                    <Text fontSize="xs" color="fg.muted">
                      Let anyone create an account via email/password. Turn off to invite-only.
                    </Text>
                  </VStack>
                  <Toggle
                    aria-label="Allow public sign-up"
                    checked={auth.allow_signup ?? true}
                    onChange={(v) => updateAuth((a) => ({ ...a, allow_signup: v }))}
                  />
                </HStack>
                <HStack justify="space-between" gap="3">
                  <VStack gap="0.5" align="start">
                    <Text fontSize="sm" color="fg">Allow anonymous sign-in</Text>
                    <Text fontSize="xs" color="fg.muted">
                      {(auth.allow_signup ?? true)
                        ? "Issue a session to users with no email/password yet."
                        : "Blocked while public sign-up is off."}
                    </Text>
                  </VStack>
                  <Toggle
                    aria-label="Allow anonymous sign-in"
                    checked={auth.allow_anonymous ?? true}
                    disabled={!(auth.allow_signup ?? true)}
                    onChange={(v) => updateAuth((a) => ({ ...a, allow_anonymous: v }))}
                  />
                </HStack>
                <HStack justify="space-between" gap="3">
                  <VStack gap="0.5" align="start">
                    <Text fontSize="sm" color="fg">Require email verification</Text>
                    <Text fontSize="xs" color="fg.muted">
                      Users must confirm their email address before their account becomes active.
                    </Text>
                  </VStack>
                  <Toggle
                    aria-label="Require email verification"
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
                  />
                </HStack>
              </VStack>
            </Section>

            <Section title="Redirect URLs" icon={Link2}>
              <VStack gap="3" align="stretch">
                <Text fontSize="xs" color="fg.muted">
                  Origins your app may be redirected to after OAuth and email flows. The
                  server's own origin and relative paths are always allowed, so add external
                  origins only (e.g. a separate front-end host). Checked here in the browser
                  only.
                </Text>
                {(auth.redirect_urls ?? []).map((url, i) => {
                  const invalid = url.trim() !== "" && !isValidRedirectOrigin(url.trim());
                  return (
                    <VStack key={i} gap="1" align="stretch">
                      <HStack gap="2">
                        <Input
                          aria-label={`Redirect URL ${i + 1}`}
                          value={url}
                          onChange={(e) => setRedirectUrl(i, e.target.value)}
                          placeholder="https://app.example.com"
                        />
                        <IconButton
                          aria-label={`Remove redirect URL ${i + 1}`}
                          size="sm"
                          variant="ghost"
                          onClick={() => removeRedirectUrl(i)}
                        >
                          <X size={16} />
                        </IconButton>
                      </HStack>
                      {invalid && (
                        <Text fontSize="xs" color="fg.error">
                          Enter an absolute http(s) origin, e.g. https://app.example.com
                        </Text>
                      )}
                    </VStack>
                  );
                })}
                <HStack
                  as="button"
                  gap="2"
                  fontSize="sm"
                  color="fg.muted"
                  onClick={addRedirectUrl}
                  _hover={{ color: "fg" }}
                  alignSelf="start"
                >
                  <Plus size={16} />
                  <Text>Add redirect URL</Text>
                </HStack>
              </VStack>
            </Section>


            <Section title="Email Templates" icon={Mails}>
              <VStack gap="4" align="stretch">
                {EMAIL_TEMPLATES.map((kind) => {
                  const template = auth.email?.templates?.[kind.key] || {
                    subject: "",
                    body: "",
                    body_file: "",
                  };
                  const setTemplate = (patch: Partial<typeof template>) =>
                    updateAuth((a) => ({
                      ...a,
                      email: {
                        ...(a.email || { verify_email: false, templates: {} }),
                        templates: {
                          ...(a.email?.templates || {}),
                          [kind.key]: { ...template, ...patch },
                        },
                      },
                    }));
                  return (
                    <Panel key={kind.key} p="4">
                      <VStack gap="3" align="stretch">
                        <HStack justify="space-between" gap="3">
                          <Text fontSize="sm" fontWeight="medium" color="fg">{kind.label}</Text>
                          <Text
                            as="code"
                            fontSize="11px"
                            fontFamily="mono"
                            color="fg.muted"
                            opacity="0.7"
                            truncate
                          >
                            {kind.vars}
                          </Text>
                        </HStack>
                        <Field label="Subject" htmlFor={`tmpl-${kind.key}-subject`}>
                          <Input
                            id={`tmpl-${kind.key}-subject`}
                            value={template.subject}
                            onChange={(e) => setTemplate({ subject: e.target.value })}
                            placeholder={kind.defaultSubject}
                          />
                        </Field>
                        <CodeEditor
                          value={template.body}
                          onChange={(val) => setTemplate({ body: val })}
                          language="text"
                          placeholder={kind.defaultBody}
                          minHeight="80px"
                        />
                      </VStack>
                    </Panel>
                  );
                })}
              </VStack>
            </Section>

            <Section
              title="OAuth Providers"
              icon={Plug2}
            >
              {(["google", "github"] as const).map((provider) => {
                const providerConfig = auth.oauth?.[provider];
                const isEnabled = !!providerConfig;
                const secretVar = OAUTH_SECRET_VARS[provider];

                return (
                  <Panel key={provider} p="4">
                    <VStack gap="4" align="stretch">
                      <HStack justify="space-between">
                        <HStack gap="2" fontSize="sm" fontWeight="medium" color="fg">
                          {provider === "google" ? <GoogleIcon size={18} /> : <GitHubIcon size={18} />}
                          <Text textTransform="capitalize">{provider}</Text>
                        </HStack>
                        <Toggle
                          aria-label={`Enable ${provider}`}
                          checked={isEnabled}
                          onChange={() => toggleOAuth(provider, isEnabled)}
                        />
                      </HStack>

                      {isEnabled && providerConfig && (
                        <VStack gap="3" align="stretch">
                          {/* one untitled list — the credential always first, then settings */}
                          <Box borderTopWidth="1px" borderColor="border">
                            <VarRow
                              label="Client secret"
                              name={envRefName(providerConfig.client_secret) ?? secretVar}
                              isSet={
                                envVarStatus[envRefName(providerConfig.client_secret) ?? secretVar]
                                  ?.set ?? false
                              }
                              canWrite={canWriteSecrets}
                              showEnvName={showEnvName}
                              inputValue={
                                pendingDotenv[
                                  envRefName(providerConfig.client_secret) ?? secretVar
                                ] ?? ""
                              }
                              onInputChange={(v) =>
                                setPendingVar(
                                  envRefName(providerConfig.client_secret) ?? secretVar,
                                  v
                                )
                              }
                            />
                          </Box>
                          {OAUTH_SETTINGS.map((field) => {
                            const value = providerConfig[field.key] ?? "";
                            const refName = envRefName(value);
                            if (refName) {
                              return (
                                <Box key={field.key} borderTopWidth="1px" borderColor="border">
                                  <VarRow
                                    label={field.label}
                                    name={refName}
                                    isSet={envVarStatus[refName]?.set ?? false}
                                    canWrite={canWriteSecrets}
                                    showEnvName={showEnvName}
                                    inputValue={pendingDotenv[refName] ?? ""}
                                    onInputChange={(v) => setPendingVar(refName, v)}
                                  />
                                </Box>
                              );
                            }
                            const id = `${provider}-${field.key}`;
                            return (
                              <Field key={field.key} label={field.label} htmlFor={id}>
                                <Input
                                  id={id}
                                  placeholder={"placeholder" in field ? field.placeholder : undefined}
                                  value={value}
                                  onChange={(e) =>
                                    updateOAuthSetting(provider, field.key, e.target.value)
                                  }
                                />
                              </Field>
                            );
                          })}
                        </VStack>
                      )}
                    </VStack>
                  </Panel>
                );
              })}
            </Section>
          </>
        ) : null}
      </VStack>

      {canWriteConfig && (
        <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
      )}
    </Box>
  );
}
