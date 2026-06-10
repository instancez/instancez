import { useState, useEffect } from "react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import type { Config } from "../lib/types";
import { Mail, HardDrive, CheckCircle2 } from "lucide-react";

const EMAIL_PROVIDERS = [
  {
    value: "resend",
    label: "Resend",
    description: "Modern email API for developers",
    envVars: ["RESEND_API_KEY"],
  },
  {
    value: "sendgrid",
    label: "SendGrid",
    description: "Twilio email delivery service",
    envVars: ["SENDGRID_API_KEY"],
  },
] as const;

const STORAGE_PROVIDERS = [
  {
    value: "s3",
    label: "AWS S3",
    description: "Amazon Simple Storage Service",
    envVars: ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION", "S3_BUCKET"],
  },
  {
    value: "gcs",
    label: "Google Cloud Storage",
    description: "Google Cloud object storage",
    envVars: ["GOOGLE_APPLICATION_CREDENTIALS", "GCS_BUCKET"],
  },
  {
    value: "minio",
    label: "MinIO",
    description: "S3-compatible object storage",
    envVars: ["MINIO_ENDPOINT", "MINIO_ACCESS_KEY", "MINIO_SECRET_KEY", "MINIO_BUCKET"],
  },
  {
    value: "local",
    label: "Local Filesystem",
    description: "Store files on the local disk",
    envVars: ["LOCAL_STORAGE_PATH"],
  },
] as const;

export function ProvidersPage() {
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

  const selectedEmail = local.providers.email?.type || null;
  const selectedStorage = local.providers.storage?.type || null;

  return (
    <div className="pb-20">
      <PageHeader
        title="Providers"
        description="Configure email and storage providers for your project"
      />

      <div className="px-8 space-y-10 max-w-3xl">
        {/* Email Provider */}
        <section className="space-y-4">
          <div className="flex items-center gap-2">
            <Mail size={16} className="text-muted-foreground" />
            <h2 className="text-sm font-semibold text-foreground">Email Provider</h2>
          </div>
          <p className="text-xs text-muted-foreground">
            Used for sending verification emails, password resets, and event notifications.
          </p>

          <div className="grid grid-cols-2 gap-3">
            {EMAIL_PROVIDERS.map((provider) => {
              const active = selectedEmail === provider.value;
              return (
                <button
                  key={provider.value}
                  type="button"
                  onClick={() =>
                    update((c) => ({
                      ...c,
                      providers: {
                        ...c.providers,
                        email: active ? null : { type: provider.value },
                      },
                    }))
                  }
                  className={`relative text-left px-4 py-3 rounded-lg border transition-all cursor-pointer ${
                    active
                      ? "border-accent bg-accent/5"
                      : "border-border bg-surface hover:border-border-hover"
                  }`}
                >
                  {active && (
                    <CheckCircle2
                      size={14}
                      className="absolute top-3 right-3 text-accent"
                    />
                  )}
                  <p className="text-sm font-medium text-foreground">{provider.label}</p>
                  <p className="text-xs text-muted-foreground mt-0.5">
                    {provider.description}
                  </p>
                </button>
              );
            })}
          </div>

          {selectedEmail && (
            <div className="rounded-lg border border-border bg-surface/50 px-4 py-3 space-y-2">
              <p className="text-xs font-medium text-foreground">Required environment variables</p>
              <div className="flex flex-wrap gap-2">
                {EMAIL_PROVIDERS.find((p) => p.value === selectedEmail)?.envVars.map(
                  (v) => (
                    <code
                      key={v}
                      className="px-2 py-0.5 rounded-sm bg-background border border-border text-xs font-mono text-muted-foreground"
                    >
                      {v}
                    </code>
                  )
                )}
              </div>
            </div>
          )}

          {!selectedEmail && (
            <p className="text-xs text-muted-foreground/60 italic">
              No email provider configured. Email-dependent features (verification, notifications) will be disabled.
            </p>
          )}
        </section>

        {/* Storage Provider */}
        <section className="space-y-4">
          <div className="flex items-center gap-2">
            <HardDrive size={16} className="text-muted-foreground" />
            <h2 className="text-sm font-semibold text-foreground">Storage Provider</h2>
          </div>
          <p className="text-xs text-muted-foreground">
            Used for file uploads and object storage via storage buckets.
          </p>

          <div className="grid grid-cols-2 gap-3">
            {STORAGE_PROVIDERS.map((provider) => {
              const active = selectedStorage === provider.value;
              return (
                <button
                  key={provider.value}
                  type="button"
                  onClick={() =>
                    update((c) => ({
                      ...c,
                      providers: {
                        ...c.providers,
                        storage: active ? null : { type: provider.value },
                      },
                    }))
                  }
                  className={`relative text-left px-4 py-3 rounded-lg border transition-all cursor-pointer ${
                    active
                      ? "border-accent bg-accent/5"
                      : "border-border bg-surface hover:border-border-hover"
                  }`}
                >
                  {active && (
                    <CheckCircle2
                      size={14}
                      className="absolute top-3 right-3 text-accent"
                    />
                  )}
                  <p className="text-sm font-medium text-foreground">{provider.label}</p>
                  <p className="text-xs text-muted-foreground mt-0.5">
                    {provider.description}
                  </p>
                </button>
              );
            })}
          </div>

          {selectedStorage && (
            <div className="rounded-lg border border-border bg-surface/50 px-4 py-3 space-y-2">
              <p className="text-xs font-medium text-foreground">Required environment variables</p>
              <div className="flex flex-wrap gap-2">
                {STORAGE_PROVIDERS.find((p) => p.value === selectedStorage)?.envVars.map(
                  (v) => (
                    <code
                      key={v}
                      className="px-2 py-0.5 rounded-sm bg-background border border-border text-xs font-mono text-muted-foreground"
                    >
                      {v}
                    </code>
                  )
                )}
              </div>
            </div>
          )}

          {!selectedStorage && (
            <p className="text-xs text-muted-foreground/60 italic">
              No storage provider configured. File upload features will be disabled.
            </p>
          )}
        </section>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
