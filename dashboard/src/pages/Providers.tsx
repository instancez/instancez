import { useState, useEffect, useCallback } from "react";
import { Box, Grid, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { SaveBar } from "../components/SaveBar";
import { CheckCard, Panel, Section, Input, Field } from "../components/ui";
import { Toggle } from "../components/Toggle";
import { VarRow } from "../components/VarRow";
import { useBackend } from "../console/BackendContext";
import { envRefName } from "../lib/envRef";
import { jsonEqual } from "../lib/jsonEqual";
import type { Config, EmailProviderConfig, StorageProviderConfig } from "../lib/types";
import { Mail, HardDrive } from "lucide-react";

// Provider schemas. Every field is a config; credentials are configs whose
// values live in .env behind a ${VAR} reference and always render first.
// Settings are literal YAML values (a hand-edited ${VAR} ref is respected
// and rendered as env-managed).
interface CredentialField {
  key: string;
  label: string;
  envVar: string;
}

interface SettingField {
  key: string;
  label: string;
  placeholder?: string;
  inputType?: string;
}

interface ProviderSchema {
  credentials: CredentialField[];
  settings: SettingField[];
}

const EMAIL_SCHEMAS: Record<string, ProviderSchema> = {
  resend: {
    credentials: [{ key: "api_key", label: "API key", envVar: "INSTANCEZ_RESEND_API_KEY" }],
    settings: [
      {
        key: "default_from_email",
        label: "Default from email",
        placeholder: "noreply@example.com",
        inputType: "email",
      },
    ],
  },
};

const STORAGE_SCHEMAS: Record<string, ProviderSchema> = {
  s3: {
    credentials: [
      { key: "access_key_id", label: "Access key ID", envVar: "AWS_ACCESS_KEY_ID" },
      { key: "secret_access_key", label: "Secret access key", envVar: "AWS_SECRET_ACCESS_KEY" },
    ],
    settings: [
      { key: "bucket", label: "Bucket", placeholder: "my-bucket" },
      { key: "region", label: "Region", placeholder: "us-east-1" },
    ],
  },
  local: {
    credentials: [],
    settings: [{ key: "path", label: "Storage path", placeholder: "./storage" }],
  },
};

// Credential vars are probed for set/unset status even before their ${VAR}
// refs are saved into the YAML (the backend only scans the YAML on its own).
const CRED_VARS = [
  ...Object.values(EMAIL_SCHEMAS),
  ...Object.values(STORAGE_SCHEMAS),
].flatMap((schema) => schema.credentials.map((c) => c.envVar));

// collectEnvRefs finds ${VAR} refs in the saved provider configs (e.g.
// hand-edited settings) so their status badges resolve too.
function collectEnvRefs(config: Config | null): string[] {
  if (!config) return [];
  const names: string[] = [];
  for (const provider of [config.providers.email, config.providers.storage]) {
    if (!provider) continue;
    for (const value of Object.values(provider)) {
      const name = envRefName(value);
      if (name) names.push(name);
    }
  }
  return names;
}

const EMAIL_PROVIDERS = [
  { value: "resend", label: "Resend", description: "Modern email API for developers" },
] as const;

const STORAGE_PROVIDERS = [
  { value: "s3", label: "AWS S3", description: "Amazon Simple Storage Service" },
  { value: "local", label: "Local Filesystem", description: "Store files on the local disk" },
] as const;

function buildEmailProvider(
  type: string,
  existing?: EmailProviderConfig | null
): EmailProviderConfig {
  const varName = EMAIL_SCHEMAS[type]?.credentials[0]?.envVar ?? "";
  return {
    type,
    api_key: `\${${varName}}`,
    default_from_email: existing?.default_from_email ?? "",
  };
}

function buildStorageProvider(
  type: string,
  explicitCreds: boolean,
  existing?: StorageProviderConfig | null
): StorageProviderConfig {
  const keep = (key: keyof StorageProviderConfig) =>
    existing?.type === type ? ((existing[key] as string) ?? "") : "";
  return {
    type,
    bucket: keep("bucket"),
    region: keep("region"),
    access_key_id: type === "s3" && explicitCreds ? "${AWS_ACCESS_KEY_ID}" : "",
    secret_access_key: type === "s3" && explicitCreds ? "${AWS_SECRET_ACCESS_KEY}" : "",
    endpoint: keep("endpoint"),
    path: keep("path"),
  };
}

interface ProviderConfigPanelProps {
  idPrefix: string;
  schema: ProviderSchema;
  provider: Record<string, unknown>;
  envVarStatus: Record<string, { set: boolean; tail?: string }>;
  pendingDotenv: Record<string, string>;
  canWriteSecrets: boolean;
  showEnvName: boolean;
  onPendingVar: (name: string, value: string) => void;
  onSettingChange: (key: string, value: string) => void;
}

// ProviderConfigPanel renders one provider's configs: credentials first
// (always), then settings. Settings holding ${VAR} refs render env-managed.
function ProviderConfigPanel({
  idPrefix,
  schema,
  provider,
  envVarStatus,
  pendingDotenv,
  canWriteSecrets,
  showEnvName,
  onPendingVar,
  onSettingChange,
}: ProviderConfigPanelProps) {
  const envManaged = schema.settings.filter((f) => envRefName(provider[f.key]));
  const literal = schema.settings.filter((f) => !envRefName(provider[f.key]));

  return (
    <Panel px="4" py="3">
      <VStack gap="3" align="stretch">
        {/* one untitled list — credentials always first, then settings */}
        {schema.credentials.length > 0 && (
          <Box borderTopWidth="1px" borderColor="border">
            {schema.credentials.map((field) => (
              <VarRow
                key={field.key}
                label={field.label}
                name={field.envVar}
                isSet={envVarStatus[field.envVar]?.set ?? false}
                tail={envVarStatus[field.envVar]?.tail}
                canWrite={canWriteSecrets}
                showEnvName={showEnvName}
                inputValue={pendingDotenv[field.envVar] ?? ""}
                onInputChange={(v) => onPendingVar(field.envVar, v)}
              />
            ))}
          </Box>
        )}
        {literal.map((field) => {
          const id = `${idPrefix}-${field.key}`;
          return (
            <Field key={field.key} label={field.label} htmlFor={id}>
              <Input
                id={id}
                type={field.inputType ?? "text"}
                placeholder={field.placeholder}
                value={(provider[field.key] as string) ?? ""}
                onChange={(e) => onSettingChange(field.key, e.target.value)}
              />
            </Field>
          );
        })}
        {envManaged.length > 0 && (
          <Box borderTopWidth="1px" borderColor="border">
            {envManaged.map((field) => {
              const name = envRefName(provider[field.key])!;
              return (
                <VarRow
                  key={field.key}
                  label={field.label}
                  name={name}
                  isSet={envVarStatus[name]?.set ?? false}
                  tail={envVarStatus[name]?.tail}
                  canWrite={canWriteSecrets}
                  showEnvName={showEnvName}
                  inputValue={pendingDotenv[name] ?? ""}
                  onInputChange={(v) => onPendingVar(name, v)}
                />
              );
            })}
          </Box>
        )}
      </VStack>
    </Panel>
  );
}

export function ProvidersPage() {
  const backend = useBackend();
  const { config, save, saving, saveErrors, dotenvWritable } = useConfig();
  const canWriteSecrets = backend.capabilities.canWriteSecrets && dotenvWritable;
  const showEnvName = backend.capabilities.showsEnvVarNames;
  const [local, setLocal] = useState<Config | null>(null);
  const [envVarStatus, setEnvVarStatus] = useState<Record<string, { set: boolean; tail?: string }>>({});
  const [pendingDotenv, setPendingDotenv] = useState<Record<string, string>>({});
  const [s3ExplicitCreds, setS3ExplicitCreds] = useState(false);

  const loadEnvVars = useCallback(async () => {
    try {
      const resp = await backend.getEnvVars([...CRED_VARS, ...collectEnvRefs(config)]);
      setEnvVarStatus(resp.vars);
    } catch {
      // secrets show as "not set" when status is unavailable
    }
  }, [backend, config]);

  useEffect(() => {
    if (config) {
      setLocal(structuredClone(config));
      const storage = config.providers.storage;
      if (storage?.type === "s3") {
        setS3ExplicitCreds(
          storage.access_key_id !== "" || storage.secret_access_key !== ""
        );
      }
    }
  }, [config]);

  useEffect(() => {
    loadEnvVars();
  }, [loadEnvVars]);

  function update(updater: (prev: Config) => Config) {
    setLocal((prev) => (prev ? updater(prev) : prev));
  }

  function setPendingVar(name: string, value: string) {
    setPendingDotenv((prev) => ({ ...prev, [name]: value }));
  }

  function selectEmailProvider(type: string | null) {
    setPendingDotenv({});
    update((c) => ({
      ...c,
      providers: {
        ...c.providers,
        email: type ? buildEmailProvider(type, c.providers.email) : null,
      },
    }));
  }

  function selectStorageProvider(type: string | null) {
    setPendingDotenv({});
    update((c) => ({
      ...c,
      providers: {
        ...c.providers,
        storage: type
          ? buildStorageProvider(type, s3ExplicitCreds, c.providers.storage)
          : null,
      },
    }));
  }

  function updateEmailSetting(key: string, value: string) {
    update((c) => ({
      ...c,
      providers: {
        ...c.providers,
        email: c.providers.email ? { ...c.providers.email, [key]: value } : c.providers.email,
      },
    }));
  }

  function updateStorageSetting(key: string, value: string) {
    update((c) => ({
      ...c,
      providers: {
        ...c.providers,
        storage: c.providers.storage
          ? { ...c.providers.storage, [key]: value }
          : c.providers.storage,
      },
    }));
  }

  function toggleS3ExplicitCreds(explicit: boolean) {
    setS3ExplicitCreds(explicit);
    update((c) => ({
      ...c,
      providers: {
        ...c.providers,
        storage:
          c.providers.storage?.type === "s3"
            ? buildStorageProvider("s3", explicit, c.providers.storage)
            : c.providers.storage,
      },
    }));
  }

  async function handleSave() {
    if (!local) return;
    const staged = Object.entries(pendingDotenv).filter(([, v]) => v !== "");
    const ok = await save(local, {
      dotenvChanges: staged.map(([name, value]) => ({
        name,
        tail: value.slice(-4),
        isUpdate: envVarStatus[name]?.set ?? false,
      })),
    });
    if (ok) {
      let wrote = false;
      if (canWriteSecrets && staged.length > 0) {
        wrote = await backend
          .writeSecrets(Object.fromEntries(staged))
          .then(() => true)
          .catch(() => false);
      }
      setPendingDotenv({});
      if (wrote) {
        // Reflect the just-written secrets optimistically. The process env
        // updates asynchronously (dev file watcher / platform env sync), so an
        // immediate refetch would race it and read the old, unset value. We know
        // exactly what we wrote, so mark it set with its tail.
        setEnvVarStatus((prev) => {
          const next = { ...prev };
          for (const [name, value] of staged) next[name] = { set: true, tail: value.slice(-4) };
          return next;
        });
      } else {
        await loadEnvVars();
      }
    }
  }

  if (!local) return null;

  // Dirty is derived, not a sticky flag: undoing an edit hides the save bar.
  const dirty =
    !jsonEqual(local, config) || Object.values(pendingDotenv).some((v) => v !== "");

  const selectedEmail = local.providers.email?.type ?? null;
  const selectedStorage = local.providers.storage?.type ?? null;
  const emailSchema = selectedEmail ? EMAIL_SCHEMAS[selectedEmail] : null;
  const baseStorageSchema = selectedStorage ? STORAGE_SCHEMAS[selectedStorage] : null;
  const storageSchema =
    baseStorageSchema && selectedStorage === "s3" && !s3ExplicitCreds
      ? { ...baseStorageSchema, credentials: [] }
      : baseStorageSchema;

  return (
    <Box pb="20">
      <VStack pb="8" gap="6" maxW="3xl" align="stretch">
        <Section
          title="Email Provider"
          icon={Mail}
        >
          <Grid gridTemplateColumns="repeat(2, 1fr)" gap="3">
            {EMAIL_PROVIDERS.map((p) => (
              <CheckCard
                key={p.value}
                selected={selectedEmail === p.value}
                onClick={() =>
                  selectEmailProvider(selectedEmail === p.value ? null : p.value)
                }
                title={p.label}
                description={p.description}
              />
            ))}
          </Grid>

          {selectedEmail && emailSchema && local.providers.email ? (
            <ProviderConfigPanel
              idPrefix="email"
              schema={emailSchema}
              provider={local.providers.email as unknown as Record<string, unknown>}
              envVarStatus={envVarStatus}
              pendingDotenv={pendingDotenv}
              canWriteSecrets={canWriteSecrets}
              showEnvName={showEnvName}
              onPendingVar={setPendingVar}
              onSettingChange={updateEmailSetting}
            />
          ) : (
            <Text fontSize="xs" color="fg.muted" opacity="0.6" fontStyle="italic">
              No email provider configured. Email-dependent features (verification, notifications)
              will be disabled.
            </Text>
          )}
        </Section>

        <Section
          title="Storage Provider"
          icon={HardDrive}
        >
          <Grid gridTemplateColumns="repeat(2, 1fr)" gap="3">
            {STORAGE_PROVIDERS.map((p) => (
              <CheckCard
                key={p.value}
                selected={selectedStorage === p.value}
                onClick={() =>
                  selectStorageProvider(selectedStorage === p.value ? null : p.value)
                }
                title={p.label}
                description={p.description}
              />
            ))}
          </Grid>

          {selectedStorage && storageSchema && local.providers.storage ? (
            <>
              {selectedStorage === "s3" && (
                <Box>
                  <Toggle
                    checked={s3ExplicitCreds}
                    onChange={toggleS3ExplicitCreds}
                    label="Provide explicit AWS credentials"
                  />
                  <Text fontSize="xs" color="fg.muted" mt="1">
                    When disabled, the AWS SDK uses instance profiles or environment defaults.
                  </Text>
                </Box>
              )}
              <ProviderConfigPanel
                idPrefix="storage"
                schema={storageSchema}
                provider={local.providers.storage as unknown as Record<string, unknown>}
                envVarStatus={envVarStatus}
                pendingDotenv={pendingDotenv}
                canWriteSecrets={canWriteSecrets}
                showEnvName={showEnvName}
                onPendingVar={setPendingVar}
                onSettingChange={updateStorageSetting}
              />
            </>
          ) : (
            <Text fontSize="xs" color="fg.muted" opacity="0.6" fontStyle="italic">
              No storage provider configured. File upload features will be disabled.
            </Text>
          )}
        </Section>
      </VStack>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </Box>
  );
}
