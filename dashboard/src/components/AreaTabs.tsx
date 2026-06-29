import { Box, HStack, Text } from "@chakra-ui/react";
import { NavLink } from "react-router-dom";
import {
  LayoutDashboard, Table2, Shield, Users, HardDrive, Code2, Database, Plug,
} from "lucide-react";

// Same areas the sidebar listed, now as horizontal tabs. `/rpc` is "Database
// Functions", `/functions` is "Code Functions". OSS keeps Providers (the
// platform omits it because providers are platform-managed).
const NAV_ITEMS = [
  { to: "/", icon: LayoutDashboard, label: "Overview" },
  { to: "/tables", icon: Table2, label: "Tables" },
  { to: "/auth", icon: Shield, label: "Auth" },
  { to: "/users", icon: Users, label: "Users" },
  { to: "/storage", icon: HardDrive, label: "Storage" },
  { to: "/rpc", icon: Database, label: "Database Functions" },
  { to: "/functions", icon: Code2, label: "Code Functions" },
  { to: "/providers", icon: Plug, label: "Providers" },
] as const;

export function AreaTabs() {
  return (
    <Box
      as="nav"
      flexShrink="0"
      mx="2"
      px="2"
      py="2"
      bg="bg.panel"
      borderWidth="1px"
      borderRadius="xl"
      boxShadow="xs"
      overflowX="auto"
      css={{ "&::-webkit-scrollbar": { display: "none" }, scrollbarWidth: "none" }}
    >
      <HStack gap="1" minW="max-content">
        {NAV_ITEMS.map(({ to, icon: Icon, label }) => (
          <NavLink key={to} to={to} end={to === "/"}>
            {({ isActive }) => (
              <Box
                display="flex"
                alignItems="center"
                gap="2"
                px="3"
                py="2"
                borderRadius="lg"
                whiteSpace="nowrap"
                fontSize="sm"
                fontWeight="medium"
                cursor="pointer"
                transition="colors"
                bg={isActive ? "bg.subtle" : "transparent"}
                color={isActive ? "fg" : "fg.muted"}
                _hover={isActive ? {} : { bg: "bg.subtle", color: "fg" }}
              >
                <Box as={Icon} boxSize="4" flexShrink="0" strokeWidth={isActive ? 2 : 1.6} />
                <Text hideBelow="sm">{label}</Text>
              </Box>
            )}
          </NavLink>
        ))}
      </HStack>
    </Box>
  );
}
