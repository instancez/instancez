import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { RefreshCw, ShieldAlert, ShieldCheck } from "lucide-react";
import { Box, chakra, Code, Flex, HStack, Text, VStack } from "@chakra-ui/react";
import { CopyButton } from "../components/ApiKeys";
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

const SECTIONS = ["tables", "storage", "rpc", "functions", "auth"];

function Ignore({ rule }: { rule: string }) {
  const snippet = `# inz-vet-ignore: ${rule}`;
  return (
    <Disclosure label="Ignore…">
      <HStack gap="2" flexWrap="wrap">
        <Code fontSize="xs" px="2" py="1" borderRadius="md" bg="bg.muted" color="fg" wordBreak="break-all">{snippet}</Code>
        <CopyButton value={snippet} label={`Copy ignore comment for ${rule}`} />
      </HStack>
    </Disclosure>
  );
}

function FindingRow({ finding, selected, onSelect }: { finding: VetFinding; selected: boolean; onSelect: () => void }) {
  const sev = bucketOf(finding.severity);
  const p = PALETTE[sev];
  const where = finding.line > 0 ? `${finding.path} · line ${finding.line}` : finding.path;
  return (
    <chakra.button
      type="button"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      display="flex" alignItems="center" gap="3" w="full" textAlign="left" px="4" py="3"
      borderBottomWidth="1px" cursor="pointer"
      bg={selected ? `${p}.subtle` : "transparent"}
      boxShadow={selected ? "inset 3px 0 0 var(--chakra-colors-" + p + "-solid)" : undefined}
      _hover={{ bg: selected ? `${p}.subtle` : "bg.subtle" }}
    >
      <Dot color={`${p}.solid`} size="2.5" />
      <Box flex="1" minW="0">
        <Text fontSize="sm" fontWeight="semibold" color="fg" truncate>{finding.title}</Text>
        <Text fontSize="xs" color="fg.muted" fontFamily="mono" truncate>{where}</Text>
      </Box>
      <Text fontSize="xs" fontWeight="semibold" color={`${p}.fg`} textTransform="uppercase" flexShrink="0">{sev}</Text>
    </chakra.button>
  );
}

function FindingDetail({ finding }: { finding: VetFinding }) {
  const section = finding.path.split(".")[0] ?? "";
  const target = SECTIONS.includes(section) ? section : null;
  return (
    <VStack align="stretch" gap="4">
      <HStack gap="2.5" flexWrap="wrap">
        <SeverityBadge severity={finding.severity} />
        <Code fontSize="xs" color="fg.muted" bg="transparent" p="0">{finding.rule}</Code>
      </HStack>
      <Text fontSize="xl" fontWeight="bold" color="fg" lineHeight="1.25" wordBreak="break-word">{finding.title}</Text>
      <Text fontSize="sm" color="fg.muted" lineHeight="1.55" wordBreak="break-word">{finding.message}</Text>
      {(finding.path || finding.line > 0) && (
        <HStack gap="2" flexWrap="wrap">
          {finding.path && <Code fontSize="xs" px="2" py="1" borderRadius="md" bg="bg.muted" color="fg" wordBreak="break-all">{finding.path}</Code>}
          {finding.line > 0 && <Text fontSize="xs" color="fg.muted" fontFamily="mono">line {finding.line}</Text>}
        </HStack>
      )}
      {finding.fix && (
        <VStack align="stretch" gap="2" p="4" borderRadius="lg" bg="green.subtle" borderWidth="1px" borderColor="green.muted">
          <Text fontSize="xs" fontWeight="bold" letterSpacing="0.04em" color="green.fg">HOW TO FIX</Text>
          <Text fontSize="sm" color="fg" whiteSpace="pre-wrap" wordBreak="break-word">{finding.fix}</Text>
        </VStack>
      )}
      <HStack gap="2" flexWrap="wrap">
        {finding.fix && <HStack gap="1" fontSize="sm" color="fg.muted">Copy fix<CopyButton value={finding.fix} label="Copy fix" /></HStack>}
        {target && (
          <Button asChild variant="outline" size="sm"><Link to={`/${target}`}>Open in {target}</Link></Button>
        )}
      </HStack>
      <Ignore rule={finding.rule} />
    </VStack>
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
  const [selKey, setSelKey] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const detailRef = useRef<HTMLDivElement>(null);
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

  const seen = new Map<string, number>();
  const visible = SEVERITIES.filter((s) => !active || s === active).flatMap((s) =>
    findings.filter((f) => bucketOf(f.severity) === s).map((finding) => {
      const base = `${finding.rule}:${finding.path}:${finding.line}`;
      const n = seen.get(base) ?? 0;
      seen.set(base, n + 1);
      return { finding, key: `${base}:${n}` };
    }),
  );
  const selected = visible.find((v) => v.key === selKey) ?? visible[0];

  function select(key: string) {
    setSelKey(key);
    const list = listRef.current?.getBoundingClientRect();
    const detail = detailRef.current;
    if (list && detail && detail.getBoundingClientRect().top >= list.bottom) detail.scrollIntoView?.({ block: "start", behavior: "smooth" });
  }

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
    body = <EmptyState icon={ShieldCheck} title="Nothing to fix" description="Your config passed every check." />;
  } else {
    body = (
      <Flex gap="5" align="flex-start" flexWrap="wrap">
        <Box ref={listRef} flex="1 1 320px" maxW={{ base: "full", xl: "520px" }} minW="0" bg="bg" borderWidth="1px" borderRadius="xl" overflow="hidden">
          {visible.map((v) => (
            <FindingRow key={v.key} finding={v.finding} selected={v.key === selected?.key} onSelect={() => { select(v.key); }} />
          ))}
        </Box>
        <Box ref={detailRef} flex="2 1 320px" minW="0" bg="bg" borderWidth="1px" borderRadius="xl" p={{ base: "5", md: "7" }}>
          {selected && <FindingDetail finding={selected.finding} />}
        </Box>
      </Flex>
    );
  }

  return (
    <Box pb="8">
      {toolbar}
      {report && !error && <Banner report={report} findings={findings} counts={counts} active={active} onFilter={(s) => { setFilter(s); setSelKey(null); }} />}
      {body}
    </Box>
  );
}
