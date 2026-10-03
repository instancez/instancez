import { Box, HStack, Menu, Portal, Text } from "@chakra-ui/react";
import { Folder, FileImage, MoreHorizontal, Download, Link2, Pencil, Trash2 } from "lucide-react";
import { formatBytes } from "../lib/utils";
import type { StorageObject, StorageFolder } from "../lib/types";

export type Action = "download" | "copyUrl" | "rename" | "delete";

function humanSize(md: StorageObject["metadata"]): string {
  return md && typeof md.size === "number" ? formatBytes(md.size) : "—";
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
            <Box as="tr" key={`d:${f.key}`} _hover={{ bg: "bg.subtle" }} cursor="pointer" onClick={() => { onOpenFolder(f); }}>
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
              <Box as="td" {...cell}>
                <Menu.Root positioning={{ placement: "bottom-end" }}>
                  <Menu.Trigger asChild>
                    <Box as="button" aria-label="Actions" p="1" borderRadius="md" color="fg.muted"
                      _hover={{ bg: "bg.muted", color: "fg" }} cursor="pointer">
                      <MoreHorizontal size={16} />
                    </Box>
                  </Menu.Trigger>
                  <Portal>
                    <Menu.Positioner>
                      <Menu.Content minW="150px">
                        {MENU.map((m) => (
                          <Menu.Item key={m.key} value={m.key} color={m.danger ? "fg.error" : "fg"} cursor="pointer"
                            onClick={() => { onAction(m.key, o); }}>
                            <Box as={m.icon} boxSize="3.5" /><Text>{m.label}</Text>
                          </Menu.Item>
                        ))}
                      </Menu.Content>
                    </Menu.Positioner>
                  </Portal>
                </Menu.Root>
              </Box>
            </Box>
          ))}
        </Box>
      </Box>
    </Box>
  );
}
