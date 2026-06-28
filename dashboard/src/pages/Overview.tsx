import React, { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Table2, Shield, HardDrive, RefreshCw, Database, Code2, ShieldAlert } from "lucide-react";
import { Box, Grid, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { useBackend } from "../console/BackendContext";
import { downloadYamlFromConfig } from "../lib/downloadYaml";
import { ApiKeys } from "../components/ApiKeys";
import { ConnectExamples } from "../components/ConnectExamples";
import { Card, CardTitle, CardValue } from "../components/Card";
import { StatusBadge } from "../components/StatusBadge";
import { Button } from "../components/ui";
import { databaseSummary, securityAdvisories } from "../lib/advisories";
import { formatBytes } from "../lib/utils";
import type { StatsResponse } from "../lib/types";

function Unit({ children }: { children: string }) {
  return (
    <Text as="span" ml="1.5" fontSize="sm" fontWeight="normal" color="fg.muted">
      {children}
    </Text>
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
  const funcCount = Object.keys(config.functions || {}).length;
  const rpcCount = Object.keys(config.rpc || {}).length;
  const authEnabled = !!config.auth;
  const exampleTable = Object.keys(config.tables || {}).sort()[0] ?? "todos";

  const totalStorage = stats
    ? Object.values(stats.storage).reduce((sum, s) => sum + s.total_bytes, 0)
    : 0;

  const tableSummary = databaseSummary(config, stats);
  const advisories = securityAdvisories(config);
  const advisoryCount = advisories.length;

  return (
    <Box pt="8" pb="8">
      <HStack align="start" justify="space-between" gap="4" pb="6">
        <Box minW="0">
          <Text fontSize="xl" fontWeight="semibold" letterSpacing="tight" color="fg" truncate>
            {config.project.name || "instancez project"}
          </Text>
          <Text mt="1" fontSize="sm" color="fg.muted">
            {config.project.description || "Project overview and health"}
          </Text>
        </Box>
        <HStack gap="2" flexShrink="0">
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
        </HStack>
      </HStack>

      <VStack gap="6" align="stretch">
        {/* Summary Cards */}
        <Grid
          gridTemplateColumns={{ base: "1fr", sm: "repeat(2, 1fr)", lg: "repeat(3, 1fr)" }}
          gap="4"
          className="animate-rise"
        >
          <Card hoverable onClick={() => navigate("tables", { relative: "path" })}>
            <HStack justify="space-between">
              <CardTitle>Tables</CardTitle>
              <Box as={Database} boxSize="4.5" color="fg.muted" />
            </HStack>
            <CardValue>
              {tableCount}
              <Unit>{tableCount === 1 ? "table" : "tables"}</Unit>
            </CardValue>
            {tableSummary.length > 0 && (
              <VStack mt="3" gap="0" align="stretch">
                {tableSummary.map(({ name, rlsCount, rowCount }) => (
                  <HStack
                    key={name}
                    justify="space-between"
                    py="1.5"
                    px="2"
                    borderRadius="md"
                    _hover={{ bg: "bg.subtle" }}
                    cursor="pointer"
                    onClick={(e: React.MouseEvent) => {
                      e.stopPropagation();
                      navigate(`tables/${name}`, { relative: "path" });
                    }}
                  >
                    <HStack gap="2">
                      <Box as={Table2} boxSize="3.5" color="fg.muted" flexShrink="0" />
                      <Text fontSize="sm" color="fg">{name}</Text>
                    </HStack>
                    <HStack gap="2">
                      {rowCount != null && (
                        <Text fontSize="xs" color="fg.muted">{rowCount} rows</Text>
                      )}
                      <StatusBadge variant={rlsCount > 0 ? "info" : "warning"}>
                        {rlsCount > 0 ? "RLS" : "exposed"}
                      </StatusBadge>
                    </HStack>
                  </HStack>
                ))}
              </VStack>
            )}
          </Card>

          <Card hoverable onClick={() => navigate("auth", { relative: "path" })}>
            <HStack justify="space-between">
              <CardTitle>Auth</CardTitle>
              <Box as={Shield} boxSize="4.5" color="fg.muted" />
            </HStack>
            <CardValue>{authEnabled ? "Enabled" : "Off"}</CardValue>
            <Text mt="1" fontSize="xs" color="fg.muted">
              {authEnabled
                ? [
                    config.auth?.email ? "Email" : null,
                    config.auth?.google ? "Google" : null,
                    config.auth?.github ? "GitHub" : null,
                  ]
                    .filter(Boolean)
                    .join(", ") || "No providers"
                : "Not configured"}
            </Text>
          </Card>

          <Card hoverable onClick={() => navigate("storage", { relative: "path" })}>
            <HStack justify="space-between">
              <CardTitle>Storage</CardTitle>
              <Box as={HardDrive} boxSize="4.5" color="fg.muted" />
            </HStack>
            <CardValue>
              {bucketCount}
              <Unit>{bucketCount === 1 ? "bucket" : "buckets"}</Unit>
            </CardValue>
            {backend.capabilities.hasStats && (
              <Text mt="1" fontSize="xs" color="fg.muted">
                {formatBytes(totalStorage)} used
              </Text>
            )}
          </Card>

          <Card hoverable onClick={() => navigate("functions", { relative: "path" })}>
            <HStack justify="space-between">
              <CardTitle>Functions</CardTitle>
              <Box as={Code2} boxSize="4.5" color="fg.muted" />
            </HStack>
            <CardValue>
              {funcCount}
              <Unit>{funcCount === 1 ? "function" : "functions"}</Unit>
            </CardValue>
            <Text mt="1" fontSize="xs" color="fg.muted">
              {rpcCount} database {rpcCount === 1 ? "function" : "functions"}
            </Text>
          </Card>

          <Card>
            <HStack justify="space-between">
              <CardTitle>
                {advisoryCount > 0
                  ? `${advisoryCount} ${advisoryCount === 1 ? "advisory" : "advisories"}`
                  : "Security"}
              </CardTitle>
              <Box
                as={ShieldAlert}
                boxSize="4.5"
                color={advisoryCount > 0 ? "orange.500" : "fg.muted"}
              />
            </HStack>
            {advisoryCount === 0 ? (
              <Text mt="2" fontSize="sm" color="fg.muted">
                No advisories
              </Text>
            ) : (
              <VStack mt="3" gap="0" align="stretch">
                {advisories.map(({ table, message }) => (
                  <HStack
                    key={table}
                    py="1.5"
                    px="2"
                    borderRadius="md"
                    _hover={{ bg: "bg.subtle" }}
                    cursor="pointer"
                    onClick={() => navigate(`tables/${table}`, { relative: "path" })}
                  >
                    <StatusBadge variant="warning">exposed</StatusBadge>
                    <Text fontSize="xs" color="orange.700" lineClamp="2">
                      {message}
                    </Text>
                  </HStack>
                ))}
              </VStack>
            )}
          </Card>
        </Grid>

        {/* API keys for connecting clients */}
        <ApiKeys />

        {/* Ready-to-paste client snippets */}
        <ConnectExamples exampleTable={exampleTable} />
      </VStack>
    </Box>
  );
}
