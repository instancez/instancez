import { useState } from "react";
import { Cable } from "lucide-react";
import { Section, useSurfaceBg } from "./ui";
import { CopyButton } from "./ApiKeys";
import { cn } from "../lib/utils";

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
  const url = window.location.origin;

  const active = CLIENT_LIBS.find((lib) => lib.id === activeId)!;
  const code = active.snippet(url, exampleTable);

  return (
    <Section title="Connect" icon={Cable}>
      <div className="flex items-center gap-1.5">
        {CLIENT_LIBS.map((lib) => (
          <button
            key={lib.id}
            onClick={() => setActiveId(lib.id)}
            className={cn(
              "px-3 py-1.5 rounded-lg text-xs font-medium transition-colors cursor-pointer",
              lib.id === activeId
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:text-foreground hover:bg-surface-hover"
            )}
          >
            {lib.label}
          </button>
        ))}
      </div>
      <div className={cn(bg, "rounded-xl border border-border overflow-hidden")}>
        <div className="flex items-center gap-2 px-4 py-2 border-b border-border">
          <code className="min-w-0 flex-1 truncate text-[11px] font-mono text-muted-foreground">
            $ {active.install}
          </code>
          <CopyButton value={active.install} label={`Copy ${active.label} install command`} />
        </div>
        <div className="flex items-start gap-2 px-4 py-3">
          <pre
            data-testid="connect-snippet"
            className="min-w-0 flex-1 overflow-x-auto text-[11px] font-mono leading-5 text-foreground"
          >
            {code}
          </pre>
          <CopyButton value={code} label={`Copy ${active.label} example`} />
        </div>
      </div>
    </Section>
  );
}
