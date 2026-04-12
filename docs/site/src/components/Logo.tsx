interface LogoProps {
  size?: number;
  className?: string;
}

export function Logo({ size = 36, className }: LogoProps) {
  const r = size * 0.22;
  const cutoutW = size * 0.4;
  const cutoutH = size * 0.74;
  const cutoutR = size * 0.2;
  const cutoutX = (size - cutoutW) / 2;
  const cutoutY = size * -0.1;

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
    >
      <defs>
        <linearGradient id="logo-grad" x1="0" y1="0" x2={size} y2={size} gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#1D1616" />
          <stop offset="50%" stopColor="#8E1616" />
          <stop offset="100%" stopColor="#D84040" />
        </linearGradient>
        <mask id="logo-mask">
          <rect width={size} height={size} rx={r} fill="white" />
          <rect
            x={cutoutX}
            y={cutoutY}
            width={cutoutW}
            height={cutoutH}
            rx={cutoutR}
            fill="black"
          />
        </mask>
      </defs>
      <rect
        width={size}
        height={size}
        rx={r}
        fill="url(#logo-grad)"
        mask="url(#logo-mask)"
      />
    </svg>
  );
}
