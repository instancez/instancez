import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Table2, Shield, HardDrive, RefreshCw } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useBackend } from "../console/BackendContext";
import { downloadYamlFromConfig } from "../lib/downloadYaml";
import { PageHeader } from "../components/PageHeader";
import { ApiKeys } from "../components/ApiKeys";
import { ConnectExamples } from "../components/ConnectExamples";
import { Card, CardTitle, CardValue } from "../components/Card";
import { Button } from "../components/ui";
import { formatBytes } from "../lib/utils";
import type { StatsResponse } from "../lib/types";

function Unit({ children }: { children: string }) {
  return (
    <span className="ml-1.5 text-sm font-normal text-muted-foreground">{children}</span>
  );
}

export function Overview() {
  const backend = useBackend();
  const { config } = useConfig();
  const navigate = useNavigate();
  const [stats, setStats] = useState<StatsResponse | null>(null);
  const [loading, setLoading] = useState(true);

  async function loadData() {
    setLoading(true);
    try {
      setStats(await backend.getStats());
    } catch {
      // Stats may fail if no tables yet
    } finally {
      setLoading(false);
    }
  }

  async function handleDownload() {
    const cfg = await backend.getConfig();
    downloadYamlFromConfig(cfg);
  }

  useEffect(() => {
    loadData();
  }, []);

  if (!config) return null;

  const tableCount = Object.keys(config.tables || {}).length;
  const bucketCount = Object.keys(config.storage || {}).length;
  const authEnabled = !!config.auth;
  const exampleTable = Object.keys(config.tables || {}).sort()[0] ?? "todos";

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
        {/* Summary Cards */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 animate-rise">
          <Card hoverable onClick={() => navigate("tables", { relative: "path" })}>
            <div className="flex items-center justify-between">
              <CardTitle>Tables</CardTitle>
              <Table2 size={18} className="text-muted-foreground" />
            </div>
            <CardValue>
              {tableCount}
              <Unit>{tableCount === 1 ? "table" : "tables"}</Unit>
            </CardValue>
          </Card>

          <Card hoverable onClick={() => navigate("auth", { relative: "path" })}>
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

          <Card hoverable onClick={() => navigate("storage", { relative: "path" })}>
            <div className="flex items-center justify-between">
              <CardTitle>Storage</CardTitle>
              <HardDrive size={18} className="text-muted-foreground" />
            </div>
            <CardValue>
              {bucketCount}
              <Unit>{bucketCount === 1 ? "bucket" : "buckets"}</Unit>
            </CardValue>
            <p className="mt-1 text-xs text-muted-foreground">
              {formatBytes(totalStorage)} used
            </p>
          </Card>
        </div>

        {/* API keys for connecting clients */}
        <ApiKeys />

        {/* Ready-to-paste client snippets */}
        <ConnectExamples exampleTable={exampleTable} />
      </div>
    </div>
  );
}
