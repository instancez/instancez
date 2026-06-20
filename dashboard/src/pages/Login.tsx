import { useState } from "react";
import { Box, Center, HStack, Input, Text, VStack } from "@chakra-ui/react";
import { KeyRound, AlertCircle } from "lucide-react";
import { validateAdminKey } from "../api/client";
import { Logo } from "../components/Logo";
import { Button, Field } from "../components/ui";

interface LoginProps {
  onSuccess: () => void;
}

export function Login({ onSuccess }: LoginProps) {
  const [key, setKey] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!key.trim()) return;
    setLoading(true);
    setError("");
    const valid = await validateAdminKey(key.trim());
    if (valid) {
      sessionStorage.setItem("instancez_admin_key", key.trim());
      onSuccess();
    } else {
      setError("Invalid admin key. Check INSTANCEZ_ADMIN_KEY.");
    }
    setLoading(false);
  }

  return (
    <Center minH="100dvh" bg="bg" px="4">
      <Box w="full" maxW="md" className="animate-rise">
        <Box
          bg="bg.panel"
          borderWidth="1px"
          borderRadius="2xl"
          boxShadow="lg"
          px="8"
          pt="10"
          pb="8"
        >
          <VStack gap="8" mb="8" textAlign="center">
            <Logo size={56} />
            <Box>
              <Text fontSize="2xl" fontWeight="bold" letterSpacing="tight" color="fg">
                Welcome back
              </Text>
              <Text mt="1.5" fontSize="sm" color="fg.muted">
                Enter your admin key to open the dashboard
              </Text>
            </Box>
          </VStack>

          <VStack as="form" onSubmit={handleSubmit} gap="5" align="stretch">
            <Field label="Admin Key" htmlFor="admin-key">
              <Box position="relative">
                <Box
                  as={KeyRound}
                  boxSize="4"
                  position="absolute"
                  left="3"
                  top="50%"
                  transform="translateY(-50%)"
                  color="fg.muted"
                  zIndex="1"
                />
                <Input
                  id="admin-key"
                  type="password"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                  placeholder="INSTANCEZ_ADMIN_KEY"
                  autoFocus
                  fontFamily="mono"
                  pl="9"
                  size="sm"
                />
              </Box>
            </Field>

            {error && (
              <HStack
                p="3"
                borderRadius="lg"
                borderWidth="1px"
                borderColor="red.300"
                bg="red.50"
                _dark={{ bg: "red.900/20", borderColor: "red.700" }}
                gap="2"
              >
                <Box as={AlertCircle} boxSize="3.5" color="fg.error" flexShrink="0" />
                <Text fontSize="xs" color="fg.error">{error}</Text>
              </HStack>
            )}

            <Button
              type="submit"
              w="full"
              disabled={loading || !key.trim()}
              loading={loading}
            >
              {loading ? "Verifying..." : "Continue"}
            </Button>
          </VStack>
        </Box>

        <Text mt="6" textAlign="center" fontSize="xs" color="fg.muted">
          The admin key is stored in{" "}
          <Box as="span" fontFamily="mono">sessionStorage</Box>{" "}
          and cleared when you close the tab.
        </Text>
      </Box>
    </Center>
  );
}
