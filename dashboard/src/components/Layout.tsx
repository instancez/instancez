import { Suspense } from "react";
import { Outlet, useMatches, useParams } from "react-router-dom";
import { Box, Center, HStack, Spinner, Text, VStack } from "@chakra-ui/react";
import { AlertCircle, RefreshCw } from "lucide-react";
import { Navbar } from "./Navbar";
import { Sidebar } from "./Sidebar";
import { PageHeader } from "./PageHeader";
import { Button, SurfaceProvider } from "./ui";
import { DriftBanner } from "./DriftBanner";
import { EditModeBanner } from "./EditModeBanner";
import { useConfigStatus } from "../hooks/useConfigStatus";
import { useConfig } from "../hooks/useConfig";
import { ConsoleProvider } from "../console/ConsoleProvider";
import { adminBackend } from "../console/adminBackend";
import type { ConsoleRouteHandle } from "../console/routes";

function PageLoader() {
  return (
    <Center py="24" flexDirection="column" gap="3">
      <Spinner size="sm" color="fg.muted" />
      <Text fontSize="sm" color="fg.muted">Loading</Text>
    </Center>
  );
}

function StatusBanners() {
  const { data } = useConfigStatus();
  return (
    <>
      <DriftBanner status={data} />
      <EditModeBanner status={data} />
    </>
  );
}

/** Page chrome: reads the deepest matched route's handle and renders the
 *  shared PageHeader once. Pages themselves are chrome-free; the shell owns
 *  the title/description. A `null` title (e.g. Overview) renders nothing. */
function ShellHeader() {
  const matches = useMatches();
  const params = useParams();
  const match = [...matches].reverse().find(
    (m) => (m.handle as ConsoleRouteHandle | undefined)?.title !== undefined
  );
  const handle = match?.handle as ConsoleRouteHandle | undefined;
  if (!handle || handle.title === null) return null;
  const title = typeof handle.title === "function" ? handle.title(params) : handle.title;
  return <PageHeader title={title} description={handle.description} />;
}

/** Inner shell: reads config state from ConsoleProvider's context for the
 *  loading/error gate, then renders the full chrome + Outlet. */
function Shell() {
  const { loading, error, config, refresh } = useConfig();

  if (loading && !config) {
    return (
      <Center minH="100dvh" bg="bg">
        <VStack gap="3">
          <Spinner size="md" color="fg.muted" />
          <Text fontSize="sm" color="fg.muted">Loading configuration...</Text>
        </VStack>
      </Center>
    );
  }

  if (error && !config) {
    return (
      <Center minH="100dvh" bg="bg">
        <VStack gap="4" maxW="sm" textAlign="center">
          <Box as={AlertCircle} boxSize="8" color="fg.error" />
          <Text fontSize="sm" color="fg.muted">{error}</Text>
          <Button onClick={refresh}>
            <RefreshCw size={14} />
            Retry
          </Button>
        </VStack>
      </Center>
    );
  }

  return (
    <Box h="100dvh" bg="bg" display="flex" flexDirection="column" overflow="hidden">
      <Navbar />
      <HStack flex="1" minH="0" gap="2" px="2" pb="2" align="stretch">
        <Sidebar />
        <Box
          as="main"
          flex="1"
          minW="0"
          overflowY="auto"
          bg="bg.panel"
          borderWidth="1px"
          borderRadius="xl"
          boxShadow="xs"
        >
          {/* Depth 1: page content sits on the surface card, so Panels
              inside it render as gray insets, and their children flip
              back to surface — every box contrasts with its parent. */}
          <SurfaceProvider depth={1}>
            {/* The shell owns the title and the horizontal gutter; pages are
                chrome-free bare content panes. */}
            <ShellHeader />
            <Box px="8">
              <Suspense fallback={<PageLoader />}>
                <Outlet />
              </Suspense>
            </Box>
          </SurfaceProvider>
        </Box>
      </HStack>
      {/* Banners anchor to the bottom of the shell as full-width strips,
          pushing the working area up rather than overlapping it. */}
      <StatusBanners />
    </Box>
  );
}

/** Route element — wraps the entire console subtree in ConsoleProvider
 *  (BackendContext + ConfigContext + DialogProvider + SaveToast +
 *  ConfirmSaveDialog), then renders the loading/error gate and chrome. */
export function Layout() {
  return (
    <ConsoleProvider backend={adminBackend}>
      <Shell />
    </ConsoleProvider>
  );
}
