import { useState } from "react";
import { Cable } from "lucide-react";
import { Box, HStack, chakra } from "@chakra-ui/react";
import { Section, useSurfaceBg } from "./ui";
import { CopyButton } from "./ApiKeys";
import { useApiBaseUrl } from "../console/BackendContext";

interface ClientLib {
  id: string;
  label: string;
  install: string;
  snippet: (url: string, table: string) => string;
}

// Snippets reference the anon key as a variable rather than inlining the raw
// token — shorter, and the value is one copy-click away in the panel above.
const CLIENT_LIBS: ClientLib[] = [
  {
    id: "js",
    label: "JS/TS",
    install: "npm install @supabase/supabase-js",
    snippet: (url, table) => `import { createClient } from '@supabase/supabase-js'

const supabase = createClient('${url}', ANON_KEY)

const { data, error } = await supabase.from('${table}').select()`,
  },
  {
    id: "python",
    label: "Python",
    install: "pip install supabase",
    snippet: (url, table) => `from supabase import create_client

supabase = create_client("${url}", ANON_KEY)

response = supabase.table("${table}").select("*").execute()`,
  },
  {
    id: "go",
    label: "Go",
    install: "go get github.com/supabase-community/supabase-go",
    snippet: (url, table) => `import supabase "github.com/supabase-community/supabase-go"

client, err := supabase.NewClient("${url}", anonKey, &supabase.ClientOptions{})

data, count, err := client.From("${table}").Select("*", "exact", false).Execute()`,
  },
];

/**
 * Ready-to-paste connection snippets for Supabase client libraries, filled in
 * with this instance's URL. instancez speaks the Supabase wire protocol, so
 * the official clients work unchanged.
 */
export function ConnectExamples({ exampleTable }: { exampleTable: string }) {
  const bg = useSurfaceBg();
  const [activeId, setActiveId] = useState(CLIENT_LIBS[0]!.id);
  const url = useApiBaseUrl();

  const active = CLIENT_LIBS.find((lib) => lib.id === activeId)!;
  const code = active.snippet(url, exampleTable);

  return (
    <Section title="Connect" icon={Cable}>
      <HStack gap="1.5">
        {CLIENT_LIBS.map((lib) => (
          <chakra.button
            key={lib.id}
            type="button"
            onClick={() => setActiveId(lib.id)}
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
          <Box
            as="code"
            minW="0"
            flex="1"
            overflow="hidden"
            textOverflow="ellipsis"
            whiteSpace="nowrap"
            fontSize="11px"
            fontFamily="mono"
            color="fg.muted"
          >
            $ {active.install}
          </Box>
          <CopyButton value={active.install} label={`Copy ${active.label} install command`} />
        </HStack>
        <HStack alignItems="start" gap="2" px="4" py="3">
          <Box
            as="pre"
            data-testid="connect-snippet"
            minW="0"
            flex="1"
            overflowX="auto"
            fontSize="11px"
            fontFamily="mono"
            lineHeight="1.5"
            color="fg"
          >
            {code}
          </Box>
          <CopyButton value={code} label={`Copy ${active.label} example`} />
        </HStack>
      </Box>
    </Section>
  );
}
