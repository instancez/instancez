import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Copy, Lightbulb, RefreshCw, ShieldAlert, ShieldCheck } from "lucide-react";
import { Box, chakra, Code, Flex, HStack, Text, VStack } from "@chakra-ui/react";
import { Card } from "../components/Card";
import { EmptyState } from "../components/EmptyState";
import { ListSkeleton } from "../components/Skeletons";
import { Button, Disclosure } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import { copyText } from "../lib/copyText";
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
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const copy = async () => {
    setState((await copyText(snippet)) ? "copied" : "failed");
    setTimeout(() => { setState("idle"); }, 1500);
  };
  return (
    <Disclosure label="Ignore">
      <HStack gap="2" flexWrap="wrap">
        <Code fontSize="xs" px="2" py="1" borderRadius="md" bg="bg.muted" color="fg" wordBreak="break-all">{snippet}</Code>
        <Button variant="outline" size="xs" onClick={() => void copy()} aria-label={`Copy ignore comment for ${rule}`}>
          {state === "copied" ? <Check size={12} /> : <Copy size={12} />}
          {state === "copied" ? "Copied" : state === "failed" ? "Copy failed" : "Copy"}
        </Button>
      </HStack>
    </Disclosure>
  );
}

function FindingCard({ finding }: { finding: VetFinding }) {
  const p = PALETTE[bucketOf(finding.severity)];
  return (
    <Card>
      <Flex gap="4" align="stretch">
        <Box w="1" flexShrink="0" borderRadius="full" bg={`${p}.solid`} alignSelf="stretch" />
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

function SeverityTile({ severity, count, active, onClick }: { severity: VetSeverity; count: number; active: boolean; onClick: () => void }) {
  const p = PALETTE[severity];
  const zero = count === 0;
  return (
    <chakra.button
      onClick={onClick}
      disabled={zero}
      aria-pressed={active}
      aria-label={`${severity} ${count}`}
      textAlign="left"
      p="3"
      borderRadius="lg"
      borderWidth="1px"
      borderColor={active ? `${p}.solid` : "border"}
      bg={active ? `${p}.subtle` : "bg.panel"}
      opacity={zero ? 0.5 : 1}
      cursor={zero ? "default" : "pointer"}
      transition="background 0.15s, border-color 0.15s"
      _hover={zero ? undefined : { borderColor: `${p}.solid` }}
    >
      <Text fontSize="2xl" fontWeight="semibold" lineHeight="1.1" fontVariantNumeric="tabular-nums" color={zero ? "fg.muted" : `${p}.fg`}>{count}</Text>
      <Text fontSize="xs" textTransform="uppercase" letterSpacing="wide" color="fg.muted">{severity}</Text>
    </chakra.button>
  );
}

export function SecurityPage() {
  const backend = useBackend();
  const [report, setReport] = useState<VetReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<VetSeverity | null>(null);
  const seq = useRef(0);

  const scan = useCallback(() => {
    const id = ++seq.current;
    setError(null);
    backend.getVetReport().then(
      (r) => { if (id === seq.current) { setReport(r); } },
      (e: unknown) => { if (id === seq.current) { setError(e instanceof Error ? e.message : String(e)); } },
    );
  }, [backend]);

  useEffect(() => {
    scan();
    return () => { seq.current++; };
  }, [scan]);

  const findings = report?.findings ?? [];
  const counts = Object.fromEntries(SEVERITIES.map((s) => [s, 0])) as Record<VetSeverity, number>;
  for (const f of findings) counts[bucketOf(f.severity)]++;
  const active = filter && counts[filter] > 0 ? filter : null;

  const toolbar = (
    <HStack justify="space-between" gap="4" pb="6" flexWrap="wrap">
      <Text fontSize="sm" color="fg.muted">
        {report ? `${findings.length} finding${findings.length !== 1 ? "s" : ""}` : "Scanning instancez.yaml"}
      </Text>
      <Button variant="outline" size="sm" onClick={scan}><RefreshCw size={14} /> Re-scan</Button>
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
        <Box display="grid" gridTemplateColumns="repeat(auto-fit, minmax(110px, 1fr))" gap="3" pb="6">
          {SEVERITIES.map((s) => (
            <SeverityTile key={s} severity={s} count={counts[s]} active={active === s}
              onClick={() => { setFilter(active === s ? null : s); }} />
          ))}
        </Box>
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
      {body}
    </Box>
  );
}
