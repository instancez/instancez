import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import {
  Table2,
  Shield,
  HardDrive,
  Zap,
  Clock,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  Server,
  RefreshCw,
} from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { getStats, getStatus } from "../api/client";
import { PageHeader } from "../components/PageHeader";
import { Card, CardTitle, CardValue } from "../components/Card";
import { StatusBadge } from "../components/StatusBadge";
import { formatBytes, formatNumber } from "../lib/utils";
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

  useEffect(() => {
    loadData();
  }, []);

  if (!config) return null;

  const tableCount = Object.keys(config.tables || {}).length;
  const bucketCount = Object.keys(config.storage || {}).length;
  const triggerCount = Object.keys(config.on || {}).length;
  const functionCount = Object.keys(config.functions || {}).length;
  const authEnabled = !!config.auth;

  const totalRows = stats
    ? Object.values(stats.tables).reduce((sum, t) => sum + t.row_count, 0)
    : 0;

  const totalStorage = stats
    ? Object.values(stats.storage).reduce((sum, s) => sum + s.total_bytes, 0)
    : 0;

  const eventsLastHour = stats?.events.last_hour;

  return (
    <div className="pb-8">
      <PageHeader
        title={config.project.name || "Ultrabase Project"}
        description={config.project.description || "Project overview and health"}
        actions={
          <button
            onClick={loadData}
            disabled={loading}
            className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </button>
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
        <div className="grid grid-cols-4 gap-4">
          <Card hoverable onClick={() => navigate("/tables")}>
            <div className="flex items-center justify-between">
              <CardTitle>Tables</CardTitle>
              <Table2 size={18} className="text-muted-foreground" />
            </div>
            <CardValue>{tableCount}</CardValue>
            <p className="mt-1 text-xs text-muted-foreground">
              {formatNumber(totalRows)} total rows
            </p>
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

          <Card hoverable onClick={() => navigate("/events")}>
            <div className="flex items-center justify-between">
              <CardTitle>Events</CardTitle>
              <Zap size={18} className="text-muted-foreground" />
            </div>
            <CardValue>{triggerCount}</CardValue>
            <p className="mt-1 text-xs text-muted-foreground">
              {triggerCount === 1 ? "trigger" : "triggers"} configured
            </p>
          </Card>
        </div>

        {/* Event Throughput */}
        {eventsLastHour && (
          <Card>
            <h3 className="text-sm font-medium text-foreground mb-4">
              Event Throughput (Last Hour)
            </h3>
            <div className="grid grid-cols-3 gap-6">
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-lg bg-accent/10 flex items-center justify-center">
                  <CheckCircle2 size={18} className="text-accent" />
                </div>
                <div>
                  <p className="text-lg font-semibold text-foreground tabular-nums">
                    {eventsLastHour.delivered}
                  </p>
                  <p className="text-xs text-muted-foreground">Delivered</p>
                </div>
              </div>
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-lg bg-warning/10 flex items-center justify-center">
                  <AlertTriangle size={18} className="text-warning" />
                </div>
                <div>
                  <p className="text-lg font-semibold text-foreground tabular-nums">
                    {eventsLastHour.failed}
                  </p>
                  <p className="text-xs text-muted-foreground">Failed</p>
                </div>
              </div>
              <div className="flex items-center gap-3">
                <div className="w-9 h-9 rounded-lg bg-destructive/10 flex items-center justify-center">
                  <XCircle size={18} className="text-destructive" />
                </div>
                <div>
                  <p className="text-lg font-semibold text-foreground tabular-nums">
                    {eventsLastHour.dead}
                  </p>
                  <p className="text-xs text-muted-foreground">Dead Letter</p>
                </div>
              </div>
            </div>
          </Card>
        )}

        {/* Tables Detail */}
        {tableCount > 0 && stats && (
          <Card>
            <h3 className="text-sm font-medium text-foreground mb-4">
              Tables
            </h3>
            <div className="space-y-2">
              {Object.entries(config.tables).map(([name, table]) => (
                <button
                  key={name}
                  onClick={() => navigate(`/tables/${name}`)}
                  className="w-full flex items-center justify-between px-3 py-2 rounded-lg hover:bg-surface-hover transition-colors cursor-pointer text-left"
                >
                  <div className="flex items-center gap-3">
                    <Table2 size={14} className="text-muted-foreground" />
                    <span className="text-sm font-mono text-foreground">
                      {name}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {Object.keys(table.fields || {}).length} fields
                    </span>
                  </div>
                  <span className="text-sm text-muted-foreground tabular-nums">
                    {formatNumber(stats.tables[name]?.row_count ?? 0)} rows
                  </span>
                </button>
              ))}
            </div>
          </Card>
        )}

        {/* Storage Buckets */}
        {bucketCount > 0 && stats && (
          <Card>
            <h3 className="text-sm font-medium text-foreground mb-4">
              Storage Buckets
            </h3>
            <div className="space-y-2">
              {Object.entries(config.storage).map(([name, bucket]) => (
                <button
                  key={name}
                  onClick={() => navigate(`/storage/${name}`)}
                  className="w-full flex items-center justify-between px-3 py-2 rounded-lg hover:bg-surface-hover transition-colors cursor-pointer text-left"
                >
                  <div className="flex items-center gap-3">
                    <HardDrive size={14} className="text-muted-foreground" />
                    <span className="text-sm font-mono text-foreground">
                      {name}
                    </span>
                    {bucket.public && (
                      <StatusBadge variant="info">public</StatusBadge>
                    )}
                  </div>
                  <div className="text-right">
                    <span className="text-sm text-muted-foreground tabular-nums">
                      {stats.storage[name]?.object_count ?? 0} objects
                    </span>
                    <span className="text-xs text-muted-foreground ml-3">
                      {formatBytes(stats.storage[name]?.total_bytes ?? 0)}
                    </span>
                  </div>
                </button>
              ))}
            </div>
          </Card>
        )}

        {/* Cron Schedules */}
        {Object.entries(config.on || {}).some(
          ([_, t]) => t.schedule
        ) && (
          <Card>
            <h3 className="text-sm font-medium text-foreground mb-4">
              Cron Schedules
            </h3>
            <div className="space-y-2">
              {Object.entries(config.on)
                .filter(([_, t]) => t.schedule)
                .map(([name, trigger]) => (
                  <div
                    key={name}
                    className="flex items-center justify-between px-3 py-2 rounded-lg"
                  >
                    <div className="flex items-center gap-3">
                      <Clock size={14} className="text-muted-foreground" />
                      <span className="text-sm font-mono text-foreground">
                        {name}
                      </span>
                    </div>
                    <span className="text-sm font-mono text-muted-foreground">
                      {trigger.schedule}
                    </span>
                  </div>
                ))}
            </div>
          </Card>
        )}
      </div>
    </div>
  );
}
