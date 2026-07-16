import React from "react";
import { Text } from "@chakra-ui/react";
import { Panel } from "./ui";

interface CardProps {
  children: React.ReactNode;
  className?: string;
  onClick?: () => void;
  hoverable?: boolean;
}

/** Padded Panel — kept as the stat/summary card shape. */
export function Card({
  children,
  className,
  onClick,
  hoverable = false,
}: CardProps) {
  return (
    <Panel
      onClick={onClick}
      hoverable={hoverable}
      p="5"
      className={className}
    >
      {children}
    </Panel>
  );
}

export function CardTitle({ children }: { children: React.ReactNode }) {
  return (
    <Text as="h3" fontSize="xs" fontWeight="semibold" color="fg.muted" textTransform="uppercase" letterSpacing="wider">
      {children}
    </Text>
  );
}

export function CardValue({ children }: { children: React.ReactNode }) {
  return (
    <Text mt="2" fontSize="3xl" fontWeight="semibold" letterSpacing="tight" color="fg" fontVariantNumeric="tabular-nums">
      {children}
    </Text>
  );
}
