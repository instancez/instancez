import { useState, useEffect } from "react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CheckCard, Panel, Section } from "../components/ui";
import type { Config } from "../lib/types";
import { Mail, HardDrive } from "lucide-react";

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

interface ProviderOption {
  readonly value: string;
  readonly label: string;
  readonly description: string;
  readonly envVars: readonly string[];
}

/** Provider picker + required-env readout, shared by both provider kinds. */
function ProviderPicker({
  options,
  selected,
  onSelect,
  emptyNote,
}: {
  options: readonly ProviderOption[];
  selected: string | null;
  onSelect: (value: string | null) => void;
  emptyNote: string;
}) {
  const active = options.find((p) => p.value === selected);
  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        {options.map((provider) => (
          <CheckCard
            key={provider.value}
            selected={selected === provider.value}
            onClick={() =>
              onSelect(selected === provider.value ? null : provider.value)
            }
            title={provider.label}
            description={provider.description}
          />
        ))}
      </div>

      {active ? (
        <Panel className="px-4 py-3 space-y-2">
          <p className="text-xs font-medium text-foreground">
            Required environment variables
          </p>
          <div className="flex flex-wrap gap-2">
            {active.envVars.map((v) => (
              <code
                key={v}
                className="px-2 py-0.5 rounded-md bg-muted border border-border text-xs font-mono text-muted-foreground"
              >
                {v}
              </code>
            ))}
          </div>
        </Panel>
      ) : (
        <p className="text-xs text-muted-foreground/60 italic">{emptyNote}</p>
      )}
    </>
  );
}

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

      <div className="px-8 pb-8 space-y-6 max-w-3xl">
        <Section
          title="Email Provider"
          description="Used for sending verification emails, password resets, and event notifications"
          icon={Mail}
        >
          <ProviderPicker
            options={EMAIL_PROVIDERS}
            selected={selectedEmail}
            onSelect={(value) =>
              update((c) => ({
                ...c,
                providers: {
                  ...c.providers,
                  email: value ? { type: value } : null,
                },
              }))
            }
            emptyNote="No email provider configured. Email-dependent features (verification, notifications) will be disabled."
          />
        </Section>

        <Section
          title="Storage Provider"
          description="Used for file uploads and object storage via storage buckets"
          icon={HardDrive}
        >
          <ProviderPicker
            options={STORAGE_PROVIDERS}
            selected={selectedStorage}
            onSelect={(value) =>
              update((c) => ({
                ...c,
                providers: {
                  ...c.providers,
                  storage: value ? { type: value } : null,
                },
              }))
            }
            emptyNote="No storage provider configured. File upload features will be disabled."
          />
        </Section>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
