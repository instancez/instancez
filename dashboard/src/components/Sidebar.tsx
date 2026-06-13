import { Box, Text, VStack } from "@chakra-ui/react";
import { NavLink } from "react-router-dom";
import {
  LayoutDashboard,
  Table2,
  Shield,
  HardDrive,
  Code2,
  Database,
  Plug,
} from "lucide-react";

const NAV_ITEMS = [
  { to: "/", icon: LayoutDashboard, label: "Overview" },
  { to: "/tables", icon: Table2, label: "Tables" },
  { to: "/auth", icon: Shield, label: "Auth" },
  { to: "/storage", icon: HardDrive, label: "Storage" },
  { to: "/rpc", icon: Database, label: "Database Functions" },
  { to: "/functions", icon: Code2, label: "Code Functions" },
  { to: "/providers", icon: Plug, label: "Providers" },
] as const;

export function Sidebar() {
  return (
    <Box
      as="aside"
      w="60"
      flexShrink="0"
      display="flex"
      flexDirection="column"
      px="3"
      py="4"
      bg="bg.panel"
      borderWidth="1px"
      borderRadius="xl"
      boxShadow="xs"
    >
      <Box as="nav" flex="1" overflowY="auto">
        <VStack gap="1" align="stretch">
          {NAV_ITEMS.map(({ to, icon: Icon, label }) => (
            <NavLink key={to} to={to} end={to === "/"}>
              {({ isActive }) => (
                <Box
                  display="flex"
                  alignItems="center"
                  gap="2.5"
                  px="3"
                  py="2"
                  borderRadius="lg"
                  fontSize="sm"
                  fontWeight="medium"
                  transition="colors"
                  cursor="pointer"
                  bg={isActive ? "fg" : "transparent"}
                  color={isActive ? "bg" : "fg.muted"}
                  _hover={isActive ? {} : { bg: "bg.subtle", color: "fg" }}
                >
                  <Box as={Icon} boxSize="4" flexShrink="0" strokeWidth={isActive ? 2 : 1.6} />
                  {label}
                </Box>
              )}
            </NavLink>
          ))}
        </VStack>
      </Box>
      <Text px="3" pt="4" fontSize="11px" color="fg.subtle" fontFamily="mono">
        v0.1.0
      </Text>
    </Box>
  );
}
