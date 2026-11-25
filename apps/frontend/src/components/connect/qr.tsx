import { useMemo } from "react";
import { encode } from "uqr";

// The QR code of a pairing, drawn in the page as an SVG: black on white with a quiet zone, also in
// the dark theme, so every phone can scan it. One path for all dark modules.
export function Qr({ text }: { text: string }) {
  const { d, size } = useMemo(() => {
    const qr = encode(text, { ecc: "L", border: 4 });
    let path = "";
    qr.data.forEach((row, y) => {
      row.forEach((dark, x) => {
        if (dark) path += `M${x} ${y}h1v1h-1z`;
      });
    });
    return { d: path, size: qr.size };
  }, [text]);

  return (
    <div
      translate="no"
      className="mx-auto w-full max-w-[272px] rounded-md border border-border bg-white p-2"
    >
      <svg
        role="img"
        aria-label="QR code"
        viewBox={`0 0 ${size} ${size}`}
        shapeRendering="crispEdges"
        className="block h-auto w-full"
      >
        <rect width={size} height={size} fill="#ffffff" />
        <path d={d} fill="#000000" />
      </svg>
    </div>
  );
}
