import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Table2, Shield, HardDrive, RefreshCw } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { getStats, getStatus, getConfig } from "../api/client";
import { downloadYamlFromConfig } from "../lib/downloadYaml";
import { PageHeader } from "../components/PageHeader";
import { ApiKeys } from "../components/ApiKeys";
import { Card, CardTitle, CardValue } from "../components/Card";
import { StatusBadge } from "../components/StatusBadge";
import { Button, ListRow, Section } from "../components/ui";
import { formatBytes } from "../lib/utils";
import type { StatsResponse } from "../lib/types";

export function Overview() {
  const { config } = useConfig();
  const navigate = useNavigate();
  const [stats, setStats] = useState<StatsResponse | null>(null);
  const [status, setStatus] = useState<Record<string, unknown> | null>(null);
  const [loading, setLoading] = useState(true);

  async function loadData() {
    setLoading(true);
    try {
      const [s, st] = await Promise.all([getStats(), getStatus()]);
      setStats(s);
      setStatus(st);
    } catch {
      // Stats may fail if no tables yet
    } finally {
      setLoading(false);
    }
  }

  async function handleDownload() {
    const cfg = await getConfig();
    downloadYamlFromConfig(cfg);
  }

  useEffect(() => {
    loadData();
  }, []);

  if (!config) return null;

  const tableCount = Object.keys(config.tables || {}).length;
  const bucketCount = Object.keys(config.storage || {}).length;
  const authEnabled = !!config.auth;

  const totalStorage = stats
    ? Object.values(stats.storage).reduce((sum, s) => sum + s.total_bytes, 0)
    : 0;

  return (
    <div className="pb-8">
      <PageHeader
        title={config.project.name || "instancez project"}
        description={config.project.description || "Project overview and health"}
        actions={
          <>
            <Button variant="outline" size="sm" onClick={handleDownload}>
              Download config as YAML
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={loadData}
              disabled={loading}
            >
              <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
              Refresh
            </Button>
          </>
        }
      />

      <div className="px-8 space-y-6">
        {/* Server Status */}
        <div className="flex items-center gap-3">
          <StatusBadge
            variant={status?.database === "connected" ? "success" : "error"}
            dot
          >
            {status?.database === "connected"
              ? "Database Connected"
              : "Database Unavailable"}
          </StatusBadge>
          {config.server.port && (
            <StatusBadge variant="info" dot>
              Port {config.server.port}
            </StatusBadge>
          )}
          {authEnabled && (
            <StatusBadge variant="success" dot>
              Auth Enabled
            </StatusBadge>
          )}
        </div>

        {/* Summary Cards */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 animate-rise">
          <Card hoverable onClick={() => navigate("/tables")}>
            <div className="flex items-center justify-between">
              <CardTitle>Tables</CardTitle>
              <Table2 size={18} className="text-muted-foreground" />
            </div>
            <CardValue>{tableCount}</CardValue>
          </Card>

          <Card hoverable onClick={() => navigate("/auth")}>
            <div className="flex items-center justify-between">
              <CardTitle>Auth</CardTitle>
              <Shield size={18} className="text-muted-foreground" />
            </div>
            <CardValue>{authEnabled ? "Enabled" : "Off"}</CardValue>
            <p className="mt-1 text-xs text-muted-foreground">
              {authEnabled
                ? [
                    config.auth?.email ? "Email" : null,
                    config.auth?.google ? "Google" : null,
                    config.auth?.github ? "GitHub" : null,
                  ]
                    .filter(Boolean)
                    .join(", ") || "No providers"
                : "Not configured"}
            </p>
          </Card>

          <Card hoverable onClick={() => navigate("/storage")}>
            <div className="flex items-center justify-between">
              <CardTitle>Storage</CardTitle>
              <HardDrive size={18} className="text-muted-foreground" />
            </div>
            <CardValue>{bucketCount}</CardValue>
            <p className="mt-1 text-xs text-muted-foreground">
              {formatBytes(totalStorage)} used
            </p>
          </Card>
        </div>

        {/* API keys for connecting clients */}
        <ApiKeys />

        {/* Tables Detail */}
        {tableCount > 0 && (
          <Section title="Tables">
            <div className="space-y-2">
              {Object.entries(config.tables).map(([name, table]) => (
                <ListRow
                  key={name}
                  icon={Table2}
                  title={name}
                  meta={`${(table.fields || []).length} fields`}
                  onClick={() => navigate(`/tables/${name}`)}
                />
              ))}
            </div>
          </Section>
        )}

        {/* Storage Buckets */}
        {bucketCount > 0 && stats && (
          <Section title="Storage Buckets">
            <div className="space-y-2">
              {Object.entries(config.storage).map(([name, bucket]) => (
                <ListRow
                  key={name}
                  icon={HardDrive}
                  title={name}
                  onClick={() => navigate(`/storage/${name}`)}
                  badges={
                    <>
                      {bucket.public && (
                        <StatusBadge variant="info">public</StatusBadge>
                      )}
                      <span className="text-sm text-muted-foreground tabular-nums">
                        {stats.storage[name]?.object_count ?? 0} objects
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {formatBytes(stats.storage[name]?.total_bytes ?? 0)}
                      </span>
                    </>
                  }
                />
              ))}
            </div>
          </Section>
        )}
      </div>
    </div>
  );
}
