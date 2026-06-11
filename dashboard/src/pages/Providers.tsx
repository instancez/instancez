import { useState, useEffect, useCallback } from "react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CheckCard, Panel, Section, Input, Field } from "../components/ui";
import { Toggle } from "../components/Toggle";
import { getEnvVars, putDotenv } from "../api/client";
import type { Config, EmailProviderConfig, StorageProviderConfig } from "../lib/types";
import { Mail, HardDrive } from "lucide-react";

const EMAIL_VARS: Record<string, string[]> = {
  resend: ["INSTANCEZ_RESEND_API_KEY"],
  sendgrid: ["INSTANCEZ_SENDGRID_API_KEY"],
};

const STORAGE_VARS: Record<string, string[]> = {
  s3: ["INSTANCEZ_S3_BUCKET", "AWS_REGION"],
  gcs: ["INSTANCEZ_GCS_BUCKET", "INSTANCEZ_GCS_CREDENTIALS"],
  minio: [
    "INSTANCEZ_MINIO_ENDPOINT",
    "INSTANCEZ_MINIO_ACCESS_KEY",
    "INSTANCEZ_MINIO_SECRET_KEY",
    "INSTANCEZ_MINIO_BUCKET",
  ],
  local: ["INSTANCEZ_LOCAL_STORAGE_PATH"],
};

const S3_EXPLICIT_CRED_VARS = ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"];

const EMAIL_PROVIDERS = [
  { value: "resend", label: "Resend", description: "Modern email API for developers" },
  { value: "sendgrid", label: "SendGrid", description: "Twilio email delivery service" },
] as const;

const STORAGE_PROVIDERS = [
  { value: "s3", label: "AWS S3", description: "Amazon Simple Storage Service" },
  { value: "gcs", label: "Google Cloud Storage", description: "Google Cloud object storage" },
  { value: "minio", label: "MinIO", description: "S3-compatible object storage" },
  { value: "local", label: "Local Filesystem", description: "Store files on the local disk" },
] as const;

function buildEmailProvider(
  type: string,
  existing?: EmailProviderConfig | null
): EmailProviderConfig {
  const varName = EMAIL_VARS[type]?.[0] ?? "";
  return {
    type,
    api_key: `\${${varName}}`,
    default_from_email: existing?.default_from_email ?? "",
  };
}

function buildStorageProvider(type: string, explicitCreds: boolean): StorageProviderConfig {
  return {
    type,
    bucket:
      type === "s3"
        ? "${INSTANCEZ_S3_BUCKET}"
        : type === "gcs"
          ? "${INSTANCEZ_GCS_BUCKET}"
          : type === "minio"
            ? "${INSTANCEZ_MINIO_BUCKET}"
            : "",
    region: type === "s3" ? "${AWS_REGION}" : "",
    access_key_id:
      type === "s3" && explicitCreds
        ? "${AWS_ACCESS_KEY_ID}"
        : type === "minio"
          ? "${INSTANCEZ_MINIO_ACCESS_KEY}"
          : "",
    secret_access_key:
      type === "s3" && explicitCreds
        ? "${AWS_SECRET_ACCESS_KEY}"
        : type === "minio"
          ? "${INSTANCEZ_MINIO_SECRET_KEY}"
          : "",
    endpoint: type === "minio" ? "${INSTANCEZ_MINIO_ENDPOINT}" : "",
    credentials: type === "gcs" ? "${INSTANCEZ_GCS_CREDENTIALS}" : "",
    path: type === "local" ? "${INSTANCEZ_LOCAL_STORAGE_PATH}" : "",
  };
}

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

export function ProvidersPage() {
  const { config, save, saving, saveErrors, dotenvWritable } = useConfig();
  const [local, setLocal] = useState<Config | null>(null);
  const [dirty, setDirty] = useState(false);
  const [envVarStatus, setEnvVarStatus] = useState<Record<string, { set: boolean }>>({});
  const [pendingDotenv, setPendingDotenv] = useState<Record<string, string>>({});
  const [s3ExplicitCreds, setS3ExplicitCreds] = useState(false);

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
      setLocal(structuredClone(config));
      setDirty(false);
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
    setLocal((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  function setPendingVar(name: string, value: string) {
    setPendingDotenv((prev) => ({ ...prev, [name]: value }));
    setDirty(true);
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
        storage: type ? buildStorageProvider(type, s3ExplicitCreds) : null,
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
            ? buildStorageProvider("s3", explicit)
            : c.providers.storage,
      },
    }));
  }

  async function handleSave() {
    if (!local) return;
    const ok = await save(local);
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

  if (!local) return null;

  const selectedEmail = local.providers.email?.type ?? null;
  const selectedStorage = local.providers.storage?.type ?? null;
  const emailVars = selectedEmail ? (EMAIL_VARS[selectedEmail] ?? []) : [];
  const baseStorageVars = selectedStorage ? (STORAGE_VARS[selectedStorage] ?? []) : [];
  const storageVars =
    selectedStorage === "s3" && s3ExplicitCreds
      ? [...baseStorageVars, ...S3_EXPLICIT_CRED_VARS]
      : baseStorageVars;

  return (
    <div className="pb-20">
      <PageHeader
        title="Providers"
        description="Configure email and storage providers for your project"
      />

      <div className="px-8 pb-8 space-y-6 max-w-3xl">
        <Section
          title="Email Provider"
          description="Used for sending verification emails, password resets, and event notifications"
          icon={Mail}
        >
          <div className="grid grid-cols-2 gap-3">
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
          </div>

          {selectedEmail ? (
            <Panel className="px-4 py-3 space-y-3">
              <Field label="Default From Email" htmlFor="default_from_email">
                <Input
                  id="default_from_email"
                  type="email"
                  placeholder="noreply@example.com"
                  value={local.providers.email?.default_from_email ?? ""}
                  onChange={(e) =>
                    update((c) => ({
                      ...c,
                      providers: {
                        ...c.providers,
                        email: c.providers.email
                          ? { ...c.providers.email, default_from_email: e.target.value }
                          : c.providers.email,
                      },
                    }))
                  }
                />
              </Field>
              <div>
                <p className="text-xs font-medium text-foreground mb-2">
                  Environment variables
                </p>
                <div className="space-y-1">
                  {emailVars.map((name) => (
                    <VarRow
                      key={name}
                      name={name}
                      isSet={envVarStatus[name]?.set ?? false}
                      canWrite={dotenvWritable}
                      inputValue={pendingDotenv[name] ?? ""}
                      onInputChange={(v) => setPendingVar(name, v)}
                    />
                  ))}
                </div>
              </div>
            </Panel>
          ) : (
            <p className="text-xs text-muted-foreground/60 italic">
              No email provider configured. Email-dependent features (verification, notifications)
              will be disabled.
            </p>
          )}
        </Section>

        <Section
          title="Storage Provider"
          description="Used for file uploads and object storage via storage buckets"
          icon={HardDrive}
        >
          <div className="grid grid-cols-2 gap-3">
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
          </div>

          {selectedStorage ? (
            <Panel className="px-4 py-3 space-y-3">
              {selectedStorage === "s3" && (
                <div>
                  <Toggle
                    checked={s3ExplicitCreds}
                    onChange={toggleS3ExplicitCreds}
                    label="Provide explicit AWS credentials"
                  />
                  <p className="text-xs text-muted-foreground mt-1">
                    When disabled, the AWS SDK uses instance profiles or environment defaults.
                  </p>
                </div>
              )}
              <div>
                <p className="text-xs font-medium text-foreground mb-2">
                  Environment variables
                </p>
                <div className="space-y-1">
                  {storageVars.map((name) => (
                    <VarRow
                      key={name}
                      name={name}
                      isSet={envVarStatus[name]?.set ?? false}
                      canWrite={dotenvWritable}
                      inputValue={pendingDotenv[name] ?? ""}
                      onInputChange={(v) => setPendingVar(name, v)}
                    />
                  ))}
                </div>
              </div>
            </Panel>
          ) : (
            <p className="text-xs text-muted-foreground/60 italic">
              No storage provider configured. File upload features will be disabled.
            </p>
          )}
        </Section>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
