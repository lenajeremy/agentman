// What shows while something works: the Mac app's Loader (Design/Loader.swift),
// a ring chasing round with its colours flowing from blue to pink, and the
// system's own spinner (ProgressView), drawn as macOS draws it.

export function Loader({ size = 11 }: { size?: number }) {
  const width = Math.max(1.6, size * 0.18);
  return <span className="ring-loader" style={{ width: size, height: size, ["--w" as string]: `${width}px` }} aria-label="Working" />;
}

/** macOS's spinning progress indicator: 16pt small, 10pt mini. */
export function Spinner({ size = 16, light = false }: { size?: number; light?: boolean }) {
  const spokes = Array.from({ length: 12 }, (_, i) => i);
  return (
    <svg className={`spinner ${light ? "light" : ""}`} width={size} height={size} viewBox="0 0 32 32" aria-label="Loading">
      {spokes.map((i) => (
        <line
          key={i}
          x1="16" y1="3.5" x2="16" y2="9.5"
          strokeWidth="2.9" strokeLinecap="round"
          transform={`rotate(${i * 30} 16 16)`}
          style={{ opacity: 0.15 + (0.85 * ((i + 1) % 12)) / 12 }}
        />
      ))}
    </svg>
  );
}
