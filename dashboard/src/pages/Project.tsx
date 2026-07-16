import { useEffect, useState } from "react";
import { Globe, Plus, X } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useConfig } from "../hooks/useConfig";
import { SaveBar } from "../components/SaveBar";
import { Input, Section } from "../components/ui";
import { useBackend } from "../console/BackendContext";
import { isValidCorsOrigin } from "../lib/corsOrigin";
import { jsonEqual } from "../lib/jsonEqual";

export function ProjectPage() {
  const backend = useBackend();
  const { config, save, saving, saveErrors } = useConfig();
  const canWriteConfig = backend.capabilities.canWriteConfig;
  const [origins, setOrigins] = useState<string[]>([]);

  useEffect(() => {
    if (config) setOrigins([...config.server.cors.origins]);
  }, [config]);

  if (!config) return null;

  // Not trimmed/filtered here: an added-but-unfilled row should surface the
  // save bar immediately (matching the redirect-URL editor), even though
  // handleSave drops it before persisting.
  const dirty = !jsonEqual(origins, config.server.cors.origins);

  async function handleSave() {
    if (!config) return;
    const cleaned = origins.map((o) => o.trim()).filter(Boolean);
    await save({ ...config, server: { ...config.server, cors: { origins: cleaned } } });
  }

  function setOrigin(index: number, value: string) {
    setOrigins((prev) => prev.map((o, i) => (i === index ? value : o)));
  }

  function addOrigin() {
    setOrigins((prev) => [...prev, ""]);
  }

  function removeOrigin(index: number) {
    setOrigins((prev) => prev.filter((_, i) => i !== index));
  }

  return (
    <Box pb="20">
      <VStack pb="8" gap="6" maxW="3xl" align="stretch">
        <Section title="CORS Origins" icon={Globe}>
          <VStack gap="3" align="stretch">
            <Text fontSize="xs" color="fg.muted">
              Browser origins allowed to read this app's API responses. Empty means
              no browser origin is allowed in production (server-to-server calls are
              unaffected). Use <Text as="span" fontFamily="mono">*</Text> to allow any
              origin.
            </Text>
            {origins.map((origin, i) => {
              const invalid = origin.trim() !== "" && !isValidCorsOrigin(origin.trim());
              return (
                <VStack key={i} gap="1" align="stretch">
                  <HStack gap="2">
                    <Input
                      aria-label={`CORS origin ${i + 1}`}
                      mono
                      value={origin}
                      onChange={(e) => setOrigin(i, e.target.value)}
                      placeholder="https://app.example.com"
                    />
                    <Box
                      as="button"
                      aria-label={`Remove CORS origin ${i + 1}`}
                      onClick={() => removeOrigin(i)}
                      display="flex"
                      alignItems="center"
                      justifyContent="center"
                      boxSize="8"
                      borderRadius="md"
                      color="fg.muted"
                      _hover={{ bg: "bg.subtle", color: "fg" }}
                    >
                      <X size={16} />
                    </Box>
                  </HStack>
                  {invalid && (
                    <Text fontSize="xs" color="fg.error">
                      Enter an absolute http(s) origin (e.g. https://app.example.com) or *
                    </Text>
                  )}
                </VStack>
              );
            })}
            <HStack
              as="button"
              gap="2"
              fontSize="sm"
              color="fg.muted"
              onClick={addOrigin}
              _hover={{ color: "fg" }}
              alignSelf="start"
            >
              <Plus size={16} />
              <Text>Add origin</Text>
            </HStack>
          </VStack>
        </Section>
      </VStack>

      {canWriteConfig && (
        <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
      )}
    </Box>
  );
}
