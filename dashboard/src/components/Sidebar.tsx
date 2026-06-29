import { Box, Text, VStack } from "@chakra-ui/react";
import { NavLink } from "react-router-dom";
import type { LucideIcon } from "lucide-react";
import {
  LayoutDashboard,
  Table2,
  Shield,
  Users,
  HardDrive,
  Code2,
  Database,
  Plug,
} from "lucide-react";

type NavItem = { to: string; icon: LucideIcon; label: string };

// Overview sits on its own above the sections — it's the project home, not part
// of any one area group.
const OVERVIEW: NavItem = { to: "/", icon: LayoutDashboard, label: "Overview" };

// The console areas, grouped into labelled sections.
const SECTIONS: { title: string; items: NavItem[] }[] = [
  {
    title: "Database",
    items: [
      { to: "/tables", icon: Table2, label: "Tables" },
      { to: "/rpc", icon: Database, label: "Database Functions" },
    ],
  },
  {
    title: "Auth",
    items: [
      { to: "/auth", icon: Shield, label: "Auth" },
      { to: "/users", icon: Users, label: "Users" },
    ],
  },
  {
    title: "Storage",
    items: [{ to: "/storage", icon: HardDrive, label: "Storage" }],
  },
  {
    title: "Code",
    items: [{ to: "/functions", icon: Code2, label: "Code Functions" }],
  },
  {
    title: "Integrations",
    items: [{ to: "/providers", icon: Plug, label: "Providers" }],
  },
];

function NavItemLink({ to, icon: Icon, label }: NavItem) {
  return (
    <NavLink to={to} end={to === "/"}>
      {({ isActive }) => (
        <Box
          display="flex"
          alignItems="center"
          gap="2"
          px="2.5"
          py="1.5"
          borderRadius="md"
          fontSize="13px"
          fontWeight="medium"
          transition="colors"
          cursor="pointer"
          bg={isActive ? "fg" : "transparent"}
          color={isActive ? "bg" : "fg.muted"}
          _hover={isActive ? {} : { bg: "bg.subtle", color: "fg" }}
        >
          <Box as={Icon} boxSize="3.5" flexShrink="0" strokeWidth={isActive ? 2 : 1.6} />
          {label}
        </Box>
      )}
    </NavLink>
  );
}

function SectionLabel({ children }: { children: string }) {
  return (
    <Text
      px="2.5"
      pt="3"
      pb="0.5"
      fontSize="10px"
      fontWeight="semibold"
      letterSpacing="wider"
      textTransform="uppercase"
      color="fg.subtle"
    >
      {children}
    </Text>
  );
}

export function Sidebar() {
  return (
    <Box
      as="aside"
      w="60"
      flexShrink="0"
      display="flex"
      flexDirection="column"
      px="2.5"
      py="3"
      bg="bg.panel"
      borderWidth="1px"
      borderRadius="xl"
      boxShadow="xs"
    >
      <Box as="nav" flex="1" overflowY="auto">
        <VStack gap="0.5" align="stretch">
          <NavItemLink {...OVERVIEW} />
        </VStack>
        {SECTIONS.map(({ title, items }) => (
          <Box key={title}>
            <SectionLabel>{title}</SectionLabel>
            <VStack gap="0.5" align="stretch">
              {items.map((item) => (
                <NavItemLink key={item.to} {...item} />
              ))}
            </VStack>
          </Box>
        ))}
      </Box>
      <Text px="2.5" pt="3" fontSize="10px" color="fg.subtle" fontFamily="mono">
        v0.1.0
      </Text>
    </Box>
  );
}
