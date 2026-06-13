import React, {
  createContext,
  useContext,
  useState,
  type ReactNode,
} from "react";
import { ChevronDown, ChevronRight, CheckCircle2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  Box,
  Button as ChakraButton,
  Collapsible,
  HStack,
  Input as ChakraInput,
  NativeSelect,
  Text,
  VStack,
  type BoxProps,
  type ButtonProps as ChakraButtonProps,
} from "@chakra-ui/react";
import { Field as ChakraField } from "@chakra-ui/react";

/* ── Surface depth context ─────────────────────────────────────────────── */

const SurfaceDepthContext = createContext(0);

export function SurfaceProvider({ depth, children }: { depth: number; children: ReactNode }) {
  return (
    <SurfaceDepthContext.Provider value={depth}>
      {children}
    </SurfaceDepthContext.Provider>
  );
}

/** Returns the Chakra bg token for the current depth. */
export function useSurfaceBg(): "bg" | "bg.panel" {
  const depth = useContext(SurfaceDepthContext);
  return depth % 2 === 0 ? "bg" : "bg.panel";
}

/* ── Panel ─────────────────────────────────────────────────────────────── */

interface PanelProps extends Omit<BoxProps, "onClick"> {
  children: ReactNode;
  onClick?: () => void;
  hoverable?: boolean;
}

export function Panel({ children, onClick, hoverable, ...rest }: PanelProps) {
  const depth = useContext(SurfaceDepthContext);
  const bg = depth % 2 === 0 ? "bg" : "bg.panel";
  return (
    <Box
      bg={bg}
      borderWidth="1px"
      borderRadius="xl"
      onClick={onClick}
      cursor={onClick || hoverable ? "pointer" : undefined}
      transition={hoverable ? "all 0.2s ease-out" : undefined}
      _hover={hoverable ? { bg: "bg.subtle", transform: "translateY(-4px)", boxShadow: "lg" } : undefined}
      {...rest}
    >
      <SurfaceDepthContext.Provider value={depth + 1}>
        {children}
      </SurfaceDepthContext.Provider>
    </Box>
  );
}

/* ── Button ─────────────────────────────────────────────────────────────── */

type ButtonVariant = "primary" | "outline" | "ghost" | "dashed" | "danger" | "danger-outline" | "danger-ghost";
type ButtonSize = "xs" | "sm" | "md" | "icon";

interface ButtonProps extends Omit<ChakraButtonProps, "size" | "variant"> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
}

const VARIANT_MAP: Record<ButtonVariant, Partial<ChakraButtonProps>> = {
  primary:          { bg: "fg", color: "bg", _hover: { opacity: 0.9 }, colorPalette: "gray" },
  outline:          { variant: "outline", colorPalette: "gray" },
  ghost:            { variant: "ghost", colorPalette: "gray" },
  dashed:           { variant: "outline", borderStyle: "dashed", colorPalette: "gray" },
  danger:           { bg: "red.600", color: "white", _hover: { bg: "red.700" } },
  "danger-outline": { variant: "outline", colorPalette: "red" },
  "danger-ghost":   { variant: "ghost", colorPalette: "red" },
};

const SIZE_MAP: Record<ButtonSize, "xs" | "sm" | "md"> = {
  xs: "xs", sm: "sm", md: "md", icon: "xs",
};

export function Button({
  variant = "primary",
  size = "md",
  loading = false,
  disabled,
  children,
  type = "button",
  ...rest
}: ButtonProps) {
  const variantProps = VARIANT_MAP[variant];
  return (
    <ChakraButton
      type={type}
      size={SIZE_MAP[size]}
      loading={loading}
      disabled={disabled || loading}
      fontWeight="medium"
      {...variantProps}
      {...rest}
    >
      {children}
    </ChakraButton>
  );
}

/* ── Input ──────────────────────────────────────────────────────────────── */

interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  mono?: boolean;
  inputSize?: "sm" | "md";
}

export function Input({ mono = false, inputSize = "md", ...rest }: InputProps) {
  return (
    <ChakraInput
      size={inputSize === "sm" ? "xs" : "sm"}
      fontFamily={mono ? "mono" : undefined}
      {...(rest as any)}
    />
  );
}

/* ── Select ─────────────────────────────────────────────────────────────── */

interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  mono?: boolean;
  inputSize?: "sm" | "md";
}

export function Select({ mono = false, inputSize = "md", children, ...rest }: SelectProps) {
  return (
    <NativeSelect.Root size={inputSize === "sm" ? "xs" : "sm"}>
      <NativeSelect.Field fontFamily={mono ? "mono" : undefined} {...(rest as any)}>
        {children}
      </NativeSelect.Field>
      <NativeSelect.Indicator />
    </NativeSelect.Root>
  );
}

/* ── Field ──────────────────────────────────────────────────────────────── */

interface FieldProps {
  label: ReactNode;
  htmlFor?: string;
  hint?: ReactNode;
  children: ReactNode;
}

export function Field({ label, htmlFor, hint, children }: FieldProps) {
  return (
    <ChakraField.Root>
      <ChakraField.Label htmlFor={htmlFor} fontSize="xs" fontWeight="semibold" color="fg.muted">
        {label}
      </ChakraField.Label>
      {children}
      {hint && <ChakraField.HelperText fontSize="xs">{hint}</ChakraField.HelperText>}
    </ChakraField.Root>
  );
}

/* ── Section ────────────────────────────────────────────────────────────── */

interface SectionProps {
  title: ReactNode;
  description?: ReactNode;
  icon?: LucideIcon;
  actions?: ReactNode;
  children?: ReactNode;
}

export function Section({ title, description, icon: Icon, actions, children }: SectionProps) {
  return (
    <Panel>
      <HStack
        justify="space-between"
        gap="4"
        px="5"
        pt="4"
        pb="3"
        borderBottomWidth={children != null ? "1px" : undefined}
      >
        <VStack align="start" gap="0.5" minW="0">
          <HStack gap="2" fontSize="sm" fontWeight="semibold" color="fg">
            {Icon && <Box as={Icon} boxSize="3.5" color="fg.muted" flexShrink="0" />}
            <Text>{title}</Text>
          </HStack>
          {description && <Text fontSize="xs" color="fg.muted">{description}</Text>}
        </VStack>
        {actions && <HStack gap="2" flexShrink="0">{actions}</HStack>}
      </HStack>
      {children != null && <Box px="5" py="4"><VStack gap="4">{children}</VStack></Box>}
    </Panel>
  );
}

/* ── ListRow ────────────────────────────────────────────────────────────── */

interface ListRowProps {
  icon: LucideIcon;
  title: string;
  meta?: ReactNode;
  badges?: ReactNode;
  onClick?: () => void;
}

export function ListRow({ icon: Icon, title, meta, badges, onClick }: ListRowProps) {
  const bg = useSurfaceBg();
  return (
    <Box
      as="button"
      onClick={onClick}
      w="full"
      display="flex"
      alignItems="center"
      justifyContent="space-between"
      gap="3"
      px="5"
      py="3.5"
      borderRadius="xl"
      borderWidth="1px"
      bg={bg}
      _hover={{ bg: "bg.subtle" }}
      transition="colors"
      cursor="pointer"
      textAlign="left"
    >
      <HStack gap="3" minW="0">
        <Box as={Icon} boxSize="4" color="fg.muted" flexShrink="0" />
        <Text fontSize="sm" fontFamily="mono" fontWeight="medium" color="fg" truncate>{title}</Text>
        {meta && <Text fontSize="xs" color="fg.muted" truncate>{meta}</Text>}
      </HStack>
      {badges && <HStack gap="2" flexShrink="0">{badges}</HStack>}
    </Box>
  );
}

/* ── CheckCard ──────────────────────────────────────────────────────────── */

interface CheckCardProps {
  selected: boolean;
  onClick: () => void;
  title: ReactNode;
  description?: ReactNode;
}

export function CheckCard({ selected, onClick, title, description }: CheckCardProps) {
  const bg = useSurfaceBg();
  return (
    <Box
      as="button"
      onClick={onClick}
      position="relative"
      textAlign="left"
      px="4"
      py="3"
      borderRadius="xl"
      borderWidth="1px"
      transition="all"
      cursor="pointer"
      bg={selected ? "transparent" : bg}
      borderColor={selected ? "fg" : "border"}
      _hover={{ borderColor: selected ? "fg" : "border.subtle" }}
    >
      {selected && (
        <Box position="absolute" top="3" right="3" as={CheckCircle2} boxSize="3.5" color="fg" />
      )}
      <Text fontSize="sm" fontWeight="medium" color="fg">{title}</Text>
      {description && <Text fontSize="xs" color="fg.muted" mt="0.5">{description}</Text>}
    </Box>
  );
}

/* ── Disclosure ─────────────────────────────────────────────────────────── */

interface DisclosureProps {
  label: ReactNode;
  defaultOpen?: boolean;
  children: ReactNode;
}

export function Disclosure({ label, defaultOpen = false, children }: DisclosureProps) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <Collapsible.Root open={open} onOpenChange={(e) => setOpen(e.open)}>
      <Box borderTopWidth="1px" pt="4">
        <Collapsible.Trigger asChild>
          <HStack as="button" gap="2" fontSize="sm" color="fg.muted" _hover={{ color: "fg" }} cursor="pointer">
            {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            {label}
          </HStack>
        </Collapsible.Trigger>
        <Collapsible.Content>
          <Box mt="3">{children}</Box>
        </Collapsible.Content>
      </Box>
    </Collapsible.Root>
  );
}
