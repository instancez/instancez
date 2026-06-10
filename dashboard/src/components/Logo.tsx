import logoUrl from "../assets/instancez-logo-only.svg";

interface LogoProps {
  size?: number;
  className?: string;
}

export function Logo({ size = 36, className }: LogoProps) {
  return (
    <img
      src={logoUrl}
      width={size}
      height={size}
      alt="instancez"
      className={className}
      style={{ filter: "invert(1)" }}
    />
  );
}
