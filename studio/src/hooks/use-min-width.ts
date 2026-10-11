import * as React from "react";

/**
 * Whether the window is at least px wide. For layouts that must mount a child
 * once — a terminal holds a connection — rather than render two copies and
 * hide one with CSS.
 */
export function useMinWidth(px: number) {
  const [wide, setWide] = React.useState(false);
  React.useEffect(() => {
    const mql = window.matchMedia(`(min-width: ${px}px)`);
    const onChange = () => setWide(mql.matches);
    onChange();
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [px]);
  return wide;
}
