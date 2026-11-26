import { useState, type ReactNode } from "react";
import { StreamLanguage } from "@codemirror/language";
import { classHighlighter, highlightCode } from "@lezer/highlight";
import { javascript } from "@codemirror/legacy-modes/mode/javascript";
import { python } from "@codemirror/legacy-modes/mode/python";
import { dart, kotlin } from "@codemirror/legacy-modes/mode/clike";
import { swift } from "@codemirror/legacy-modes/mode/swift";
import { go } from "@codemirror/legacy-modes/mode/go";
import { Cable } from "lucide-react";
import { Alert, Box, HStack, Text, chakra } from "@chakra-ui/react";
import { Section, useSurfaceBg } from "./ui";
import { CopyButton } from "./CopyButton";
import { useApiBaseUrl } from "../console/BackendContext";

interface ClientLib {
  id: string;
  label: string;
  install: string;
  /** False when the install line isn't a shell command (Gradle). */
  shell?: boolean;
  language: StreamLanguage<unknown>;
  snippet: (url: string, table: string, key?: string) => string;
}

// Without a key the snippets use a variable, so the raw token is never inlined by default.
export const CLIENT_LIBS: ClientLib[] = [
  {
    id: "js",
    label: "JS/TS",
    install: "npm install @supabase/supabase-js",
    language: StreamLanguage.define(javascript),
    snippet: (url, table, key) => `import { createClient } from '@supabase/supabase-js'

const supabase = createClient(
  '${url}',
  ${key ? `'${key}'` : "PUBLISHABLE_KEY"},
)

const { data, error } = await supabase
  .from('${table}')
  .select()`,
  },
  {
    id: "python",
    label: "Python",
    install: "pip install supabase",
    language: StreamLanguage.define(python),
    snippet: (url, table, key) => `from supabase import create_client

supabase = create_client(
    "${url}",
    ${key ? `"${key}"` : "PUBLISHABLE_KEY"},
)

response = supabase.table("${table}").select("*").execute()`,
  },
  {
    id: "dart",
    label: "Dart",
    install: "dart pub add supabase",
    language: StreamLanguage.define(dart),
    snippet: (url, table, key) => `import 'package:supabase/supabase.dart';

final supabase = SupabaseClient(
  '${url}',
  ${key ? `'${key}'` : "publishableKey"},
);

final data = await supabase.from('${table}').select();`,
  },
  {
    id: "swift",
    label: "Swift",
    install: "swift package add-dependency https://github.com/supabase/supabase-swift --from 2.0.0",
    language: StreamLanguage.define(swift),
    snippet: (url, table, key) => `import Supabase

let supabase = SupabaseClient(
  supabaseURL: URL(string: "${url}")!,
  supabaseKey: ${key ? `"${key}"` : "publishableKey"}
)

let response = try await supabase
  .from("${table}")
  .select()
  .execute()`,
  },
  {
    id: "kotlin",
    label: "Kotlin",
    install: `implementation("io.github.jan-tennert.supabase:postgrest-kt:3.9.0")\nimplementation("io.ktor:ktor-client-cio:3.6.0")`,
    shell: false,
    language: StreamLanguage.define(kotlin),
    snippet: (url, table, key) => `import io.github.jan.supabase.createSupabaseClient
import io.github.jan.supabase.postgrest.Postgrest
import io.github.jan.supabase.postgrest.from

val supabase = createSupabaseClient(
    supabaseUrl = "${url}",
    supabaseKey = ${key ? `"${key}"` : "publishableKey"},
) {
    install(Postgrest)
}

val rows = supabase.from("${table}").select().data`,
  },
  {
    id: "go",
    label: "Go",
    install: "go get github.com/supabase-community/supabase-go",
    language: StreamLanguage.define(go),
    snippet: (url, table, key) => `import supabase "github.com/supabase-community/supabase-go"

client, err := supabase.NewClient(
	"${url}",
	${key ? `"${key}"` : "publishableKey"},
	&supabase.ClientOptions{},
)

data, count, err := client.From("${table}").
	Select("*", "exact", false).
	Execute()`,
  },
];

/** Static syntax highlighting: `tok-*` spans colored by index.css, one block per line so wraps hang under their indent. */
export function highlight(code: string, language: StreamLanguage<unknown>): ReactNode[] {
  const lines: ReactNode[][] = [[]];
  highlightCode(
    code,
    language.parser.parse(code),
    classHighlighter,
    (text, classes) => {
      const line = lines[lines.length - 1]!;
      line.push(classes ? <span key={line.length} className={classes}>{text}</span> : text);
    },
    () => lines.push([]),
  );
  const source = code.split("\n");
  return lines.map((parts, i) => {
    const text = source[i] ?? "";
    const indent = text.match(/^\s*/)![0].replace(/\t/g, "    ").length + 2;
    return (
      <span key={i} style={{ display: "block", minHeight: "1lh", paddingLeft: `${indent}ch`, textIndent: `-${indent}ch` }}>
        {parts}
        {i < lines.length - 1 && "\n"}
      </span>
    );
  });
}

/** Client tabs plus install line and snippet for the given URL; inlines `publishableKey` when passed. */
export function ClientSnippets({ url, table, publishableKey }: { url: string; table: string; publishableKey?: string }) {
  const bg = useSurfaceBg();
  const [activeId, setActiveId] = useState(CLIENT_LIBS[0]!.id);
  const active = CLIENT_LIBS.find((lib) => lib.id === activeId)!;
  const code = active.snippet(url, table, publishableKey);

  return (
    <>
      <HStack gap="1.5" flexWrap="wrap">
        {CLIENT_LIBS.map((lib) => (
          <chakra.button
            key={lib.id}
            type="button"
            onClick={() => setActiveId(lib.id)}
            aria-pressed={lib.id === activeId}
            px="3"
            py="1.5"
            borderRadius="lg"
            fontSize="xs"
            fontWeight="medium"
            transition="colors"
            cursor="pointer"
            bg={lib.id === activeId ? "fg" : "transparent"}
            color={lib.id === activeId ? "bg" : "fg.muted"}
            _hover={lib.id === activeId ? undefined : { color: "fg", bg: "bg.subtle" }}
          >
            {lib.label}
          </chakra.button>
        ))}
      </HStack>
      <Box bg={bg} borderRadius="xl" borderWidth="1px" overflow="hidden">
        <HStack gap="2" px="4" py="2" borderBottomWidth="1px">
          <Box as="code" minW="0" flex="1" whiteSpace="pre-wrap" overflowWrap="anywhere" fontSize="11px" fontFamily="mono" color="fg.muted">
            {active.shell === false ? active.install : `$ ${active.install}`}
          </Box>
          <CopyButton value={active.install} label={`${active.label} install command`} />
        </HStack>
        <HStack alignItems="start" gap="2" px="4" py="3">
          <Box
            as="pre"
            className="code-snippet"
            data-testid="connect-snippet"
            minW="0"
            flex="1"
            whiteSpace="pre-wrap"
            overflowWrap="anywhere"
            fontSize="11px"
            fontFamily="mono"
            lineHeight="1.5"
            color="fg"
            style={{ tabSize: 4 }}
          >
            {highlight(code, active.language)}
          </Box>
          <CopyButton value={code} label={`${active.label} example`} />
        </HStack>
      </Box>
    </>
  );
}

/**
 * Ready-to-paste connection snippets for Supabase client libraries, filled in
 * with this instance's URL. instancez speaks the Supabase wire protocol, so
 * the official clients work unchanged.
 */
export function ConnectExamples({ exampleTable }: { exampleTable: string }) {
  const url = useApiBaseUrl();
  return (
    <Section title="Connect" icon={Cable}>
      <ClientSnippets url={url} table={exampleTable} />
      <Alert.Root status="warning" variant="subtle" size="sm" borderRadius="lg">
        <Alert.Indicator />
        <Alert.Description>
          Set <Text as="span" fontFamily="mono">PUBLISHABLE_KEY</Text> to the publishable key above.{" "}
          <Text as="strong" fontWeight="semibold">Never ship the secret key in browser code.</Text>
        </Alert.Description>
      </Alert.Root>
    </Section>
  );
}
