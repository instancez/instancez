import { useState } from "react";
import { Box, HStack, Text } from "@chakra-ui/react";
import { Folder, FileImage, MoreHorizontal, Download, Link2, Pencil, Trash2 } from "lucide-react";
import type { StorageObject, StorageFolder } from "../lib/types";

type Action = "download" | "copyUrl" | "rename" | "delete";

function humanSize(md: StorageObject["metadata"]): string {
  const n = md && typeof md.size === "number" ? md.size : null;
  if (n == null) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1048576) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1048576).toFixed(1)} MB`;
}

const MENU: { key: Action; label: string; icon: typeof Download; danger?: boolean }[] = [
  { key: "download", label: "Download", icon: Download },
  { key: "copyUrl", label: "Copy URL", icon: Link2 },
  { key: "rename", label: "Rename", icon: Pencil },
  { key: "delete", label: "Delete", icon: Trash2, danger: true },
];

const cell = { px: "3", py: "2.5", borderBottomWidth: "1px", borderColor: "border" } as const;

export function ObjectTable(props: {
  folders: StorageFolder[];
  objects: StorageObject[];
  onOpenFolder: (f: StorageFolder) => void;
  onAction: (a: Action, o: StorageObject) => void;
}) {
  const { folders, objects, onOpenFolder, onAction } = props;
  const [open, setOpen] = useState<string | null>(null);

  return (
    <Box overflow="auto">
      <Box as="table" width="100%" fontSize="sm" css={{ borderCollapse: "collapse" }}>
        <Box as="thead">
          <Box as="tr" bg="bg.subtle" color="fg.muted" fontSize="xs">
            <Box as="th" {...cell} textAlign="left">Name</Box>
            <Box as="th" {...cell} textAlign="left" width="120px">Size</Box>
            <Box as="th" {...cell} textAlign="left" width="180px">Type</Box>
            <Box as="th" {...cell} width="48px" />
          </Box>
        </Box>
        <Box as="tbody">
          {folders.map((f) => (
            <Box as="tr" key={`d:${f.key}`} _hover={{ bg: "bg.subtle" }} cursor="pointer" onClick={() => onOpenFolder(f)}>
              <Box as="td" {...cell}>
                <HStack gap="2"><Box as={Folder} boxSize="4" color="fg.muted" /><Text fontFamily="mono">{f.name}</Text></HStack>
              </Box>
              <Box as="td" {...cell} color="fg.muted">—</Box>
              <Box as="td" {...cell} color="fg.muted">folder</Box>
              <Box as="td" {...cell} />
            </Box>
          ))}
          {objects.map((o) => (
            <Box as="tr" key={`o:${o.id || o.name}`} _hover={{ bg: "bg.subtle" }}>
              <Box as="td" {...cell}>
                <HStack gap="2"><Box as={FileImage} boxSize="4" color="fg.muted" /><Text fontFamily="mono">{o.name}</Text></HStack>
              </Box>
              <Box as="td" {...cell} color="fg.muted" fontFamily="mono">{humanSize(o.metadata)}</Box>
              <Box as="td" {...cell} color="fg.muted" fontFamily="mono">{o.metadata?.mimetype ?? "—"}</Box>
              <Box as="td" {...cell} position="relative">
                <Box
                  as="button"
                  aria-label="Actions"
                  p="1"
                  borderRadius="md"
                  color="fg.muted"
                  _hover={{ bg: "bg.muted", color: "fg" }}
                  cursor="pointer"
                  onClick={() => setOpen((k) => (k === (o.id || o.name) ? null : o.id || o.name))}
                >
                  <MoreHorizontal size={16} />
                </Box>
                {open === (o.id || o.name) && (
                  <>
                    <Box position="fixed" inset="0" zIndex="1" onClick={() => setOpen(null)} />
                    <Box
                      position="absolute" right="2" top="100%" zIndex="2" minW="150px"
                      bg="bg.panel" borderWidth="1px" borderColor="border" borderRadius="lg" boxShadow="lg" py="1"
                    >
                      {MENU.map((m) => (
                        <HStack
                          as="button" key={m.key} w="full" gap="2" px="3" py="1.5"
                          fontSize="sm" cursor="pointer"
                          color={m.danger ? "fg.error" : "fg"}
                          _hover={{ bg: "bg.subtle" }}
                          onClick={() => { setOpen(null); onAction(m.key, o); }}
                        >
                          <Box as={m.icon} boxSize="3.5" /><Text>{m.label}</Text>
                        </HStack>
                      ))}
                    </Box>
                  </>
                )}
              </Box>
            </Box>
          ))}
        </Box>
      </Box>
    </Box>
  );
}
