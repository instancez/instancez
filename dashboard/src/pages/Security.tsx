import { useCallback, useEffect, useRef, useState } from "react";
import { Lightbulb, RefreshCw, ShieldAlert, ShieldCheck } from "lucide-react";
import { Box, chakra, Code, Flex, HStack, Text, VStack } from "@chakra-ui/react";
import { CopyButton } from "../components/ApiKeys";
import { Card } from "../components/Card";
import { EmptyState } from "../components/EmptyState";
import { ListSkeleton } from "../components/Skeletons";
import { Button, Disclosure } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import type { VetFinding, VetReport, VetSeverity } from "../lib/types";

const SEVERITIES: VetSeverity[] = ["critical", "high", "medium", "low", "info"];

const PALETTE: Record<VetSeverity, string> = {
  critical: "red",
  high: "orange",
  medium: "yellow",
  low: "blue",
  info: "gray",
};

/** Unknown severities from a newer server fall into the info bucket. */
function bucketOf(severity: string): VetSeverity {
  return (SEVERITIES as string[]).includes(severity) ? (severity as VetSeverity) : "info";
}

function SeverityBadge({ severity }: { severity: string }) {
  const p = PALETTE[bucketOf(severity)];
  return (
    <Box
      as="span"
      px="2"
      py="0.5"
      borderRadius="md"
      borderWidth="1px"
      fontSize="xs"
      fontWeight="semibold"
      textTransform="uppercase"
      letterSpacing="wide"
      bg={`${p}.subtle`}
      color={`${p}.fg`}
      borderColor={`${p}.muted`}
    >
      {severity}
    </Box>
  );
}

function IgnoreSnippet({ rule }: { rule: string }) {
  const snippet = `# inz-vet-ignore: ${rule}`;
  return (
    <Disclosure label="Ignore">
      <HStack gap="2" flexWrap="wrap">
        <Code fontSize="xs" px="2" py="1" borderRadius="md" bg="bg.muted" color="fg" wordBreak="break-all">{snippet}</Code>
        <CopyButton value={snippet} label={`Copy ignore comment for ${rule}`} />
      </HStack>
    </Disclosure>
  );
}

function FindingCard({ finding }: { finding: VetFinding }) {
  const p = PALETTE[bucketOf(finding.severity)];
  return (
    <Card>
      <Flex gap="4" align="stretch">
        <Box aria-hidden w="1" flexShrink="0" borderRadius="full" bg={`${p}.solid`} alignSelf="stretch" />
        <VStack align="stretch" gap="3" flex="1" minW="0">
          <HStack gap="2" flexWrap="wrap">
            <SeverityBadge severity={finding.severity} />
            <Code fontSize="xs" color="fg.muted" bg="transparent" p="0">{finding.rule}</Code>
          </HStack>
          <Text fontSize="md" fontWeight="semibold" color="fg">{finding.title}</Text>
          <Text fontSize="sm" color="fg.muted">{finding.message}</Text>
          {(finding.path || finding.line > 0) && (
            <HStack gap="2" flexWrap="wrap">
              {finding.path && (
                <Code fontSize="xs" px="2" py="0.5" borderRadius="md" bg="bg.muted" color="fg" wordBreak="break-all">{finding.path}</Code>
              )}
              {finding.line > 0 && <Text fontSize="xs" color="fg.muted">line {finding.line}</Text>}
            </HStack>
          )}
          {finding.fix && (
            <HStack align="flex-start" gap="2" p="3" borderRadius="md" bg="green.subtle" color="green.fg" borderWidth="1px" borderColor="green.muted">
              <Box mt="0.5" flexShrink="0"><Lightbulb size={14} /></Box>
              <Text fontSize="sm"><b>Fix:</b> {finding.fix}</Text>
            </HStack>
          )}
          <IgnoreSnippet rule={finding.rule} />
        </VStack>
      </Flex>
    </Card>
  );
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;

function Dot({ color, size = "2" }: { color: string; size?: string }) {
  return <Box as="span" aria-hidden w={size} h={size} borderRadius="full" bg={color} flexShrink="0" display="inline-block" />;
}

function SeverityChip({ severity, count, active, onClick }: { severity: VetSeverity; count: number; active: boolean; onClick: () => void }) {
  const zero = count === 0;
  return (
    <chakra.button
      type="button"
      onClick={onClick}
      disabled={zero}
      aria-pressed={active}
      display="inline-flex"
      alignItems="center"
      gap="2"
      h="8"
      px="3"
      borderRadius="md"
      borderWidth="1px"
      borderColor={active ? "#6b6a66" : "#3a3935"}
      bg={active ? "#2a2926" : "transparent"}
      color="white"
      fontSize="sm"
      fontWeight="medium"
      opacity={zero ? 0.4 : 1}
      cursor={zero ? "default" : "pointer"}
      _hover={zero ? undefined : { bg: "#2a2926" }}
    >
      <Dot color={`${PALETTE[severity]}.solid`} />
      {count} {severity}
    </chakra.button>
  );
}

function Banner({ report, findings, counts, active, onFilter }: {
  report: VetReport; findings: VetFinding[]; counts: Record<VetSeverity, number>;
  active: VetSeverity | null; onFilter: (s: VetSeverity | null) => void;
}) {
  const total = findings.length;
  const checks = report.checks;
  const worst = SEVERITIES.find((s) => counts[s] > 0);
  const rules = new Set(findings.map((f) => f.rule)).size;
  const lead = counts.critical > 0 ? `${counts.critical} critical` : counts.high > 0 ? `${counts.high} high` : null;
  const leadN = counts.critical > 0 ? counts.critical : counts.high;
  const summary = total === 0 ? "" : `${plural(total, "finding")} across ${plural(rules, "check")}${lead ? ` · start with the ${lead} ${leadN === 1 ? "one" : "ones"}` : ""}`;
  const status = worst ? "Needs attention" : checks ? "All checks passed" : "No findings";

  return (
    <Flex bg="#1b1b1a" color="white" borderRadius="2xl" px={{ base: "5", md: "7" }} py="6" gap={{ base: "5", md: "9" }} align="center" flexWrap="wrap" mb="6">
      <Box flexShrink="0">
        <Text fontSize="xs" fontWeight="bold" letterSpacing="0.05em" color="#a8a7a2">RESULT</Text>
        <Flex align="baseline" gap="2" mt="1.5">
          {checks ? (
            <>
              <Text as="span" fontSize="5xl" fontWeight="bold" letterSpacing="-0.03em" lineHeight="1">{checks.passed}</Text>
              <Text as="span" fontSize="xl" color="#a8a7a2">of {checks.total} checks passed</Text>
            </>
          ) : total > 0 ? (
            <>
              <Text as="span" fontSize="5xl" fontWeight="bold" letterSpacing="-0.03em" lineHeight="1">{total}</Text>
              <Text as="span" fontSize="xl" color="#a8a7a2">{total === 1 ? "finding" : "findings"}</Text>
            </>
          ) : (
            <Text as="span" fontSize="4xl" fontWeight="bold" letterSpacing="-0.03em" lineHeight="1">All clear</Text>
          )}
        </Flex>
      </Box>
      <VStack align="stretch" gap="3.5" flex="1" minW="260px">
        <Flex align="center" gap="2.5" flexWrap="wrap">
          <Dot color={worst ? `${PALETTE[worst]}.solid` : "green.solid"} />
          <Text fontWeight="bold">{status}</Text>
          {summary && <Text fontSize="sm" color="#a8a7a2">{summary}</Text>}
        </Flex>
        <Flex aria-hidden h="2.5" borderRadius="md" overflow="hidden" gap="0.5">
          {checks && checks.passed > 0 && <Box flex={checks.passed} bg="green.solid" />}
          {SEVERITIES.filter((s) => counts[s] > 0).map((s) => <Box key={s} flex={counts[s]} bg={`${PALETTE[s]}.solid`} />)}
        </Flex>
        <Flex gap="2" flexWrap="wrap" align="center">
          <chakra.button
            type="button"
            onClick={() => { onFilter(null); }}
            aria-pressed={active === null}
            display="inline-flex" alignItems="center" gap="2" h="8" px="3" borderRadius="md" borderWidth="1px"
            borderColor={active === null ? "#6b6a66" : "#3a3935"} bg={active === null ? "#2a2926" : "transparent"}
            color="white" fontSize="sm" fontWeight="medium" cursor="pointer" _hover={{ bg: "#2a2926" }}
          >
            <Dot color="#a8a7a2" />All {total}
          </chakra.button>
          {SEVERITIES.map((s) => (
            <SeverityChip key={s} severity={s} count={counts[s]} active={active === s} onClick={() => { onFilter(active === s ? null : s); }} />
          ))}
          {checks && (
            <Flex ml="auto" align="center" gap="2" fontSize="sm" color="#a8a7a2"><Dot color="green.solid" />{checks.passed} passed</Flex>
          )}
        </Flex>
      </VStack>
    </Flex>
  );
}

export function SecurityPage() {
  const backend = useBackend();
  const [report, setReport] = useState<VetReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<VetSeverity | null>(null);
  const [scanning, setScanning] = useState(true);
  const seq = useRef(0);

  const scan = useCallback(() => {
    const id = ++seq.current;
    setError(null);
    setScanning(true);
    backend.getVetReport().then(
      (r) => { if (id === seq.current) { setReport(r); setScanning(false); } },
      (e: unknown) => { if (id === seq.current) { setError(e instanceof Error ? e.message : String(e)); setScanning(false); } },
    );
  }, [backend]);

  useEffect(() => {
    scan();
    return () => { seq.current++; };
  }, [scan]);

  const findings = report?.findings ?? [];
  const counts = Object.fromEntries(SEVERITIES.map((s) => [s, report?.counts[s] ?? 0])) as Record<VetSeverity, number>;
  for (const f of findings) if (!(f.severity in counts)) counts.info++;
  const active = filter && counts[filter] > 0 ? filter : null;

  useEffect(() => {
    if (filter && !active) setFilter(null);
  }, [filter, active]);

  const toolbar = (
    <HStack justify="space-between" gap="4" pb="6" flexWrap="wrap">
      <Text fontSize="sm" color="fg.muted">{report && !error ? "Checked" : "Scanning"} <Code fontSize="xs">instancez.yaml</Code></Text>
      <Button variant="outline" size="sm" onClick={scan} disabled={scanning} aria-busy={scanning}><RefreshCw size={14} /> Re-scan</Button>
    </HStack>
  );

  let body;
  if (error) {
    body = (
      <EmptyState icon={ShieldAlert} title="Couldn't scan the config" description={error}
        action={<Button variant="outline" onClick={scan}>Retry</Button>} />
    );
  } else if (!report) {
    body = <ListSkeleton rows={4} />;
  } else if (findings.length === 0) {
    body = <EmptyState icon={ShieldCheck} title="No security findings" description="Your config passed every check." />;
  } else {
    body = (
      <>
        <VStack gap="3" align="stretch">
          {SEVERITIES.filter((s) => !active || s === active).flatMap((s) =>
            findings.filter((f) => bucketOf(f.severity) === s).map((f, i) => (
              <FindingCard key={`${f.rule}:${f.path}:${f.line}:${i}`} finding={f} />
            )),
          )}
        </VStack>
      </>
    );
  }

  return (
    <Box pb="8">
      {toolbar}
      {report && !error && <Banner report={report} findings={findings} counts={counts} active={active} onFilter={setFilter} />}
      {body}
    </Box>
  );
}
