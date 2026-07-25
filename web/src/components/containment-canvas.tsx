"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
} from "react";
import { cn } from "@/lib/utils";

export type CanvasHandle = {
  /** Send a command at the wall. Returns nothing; the canvas tells the story. */
  launch: (label: string, passes: boolean) => void;
};

type Shot = {
  x: number;
  y: number;
  vx: number;
  vy: number;
  label: string;
  passes: boolean;
  /** 0 = in flight, 1 = resolved. */
  state: 0 | 1;
  life: number;
};

type Shard = { x: number; y: number; vx: number; vy: number; r: number; life: number };
type Ripple = { x: number; y: number; t: number; passes: boolean };
type Mote = { x: number; y: number; vx: number; vy: number; r: number };

const MAX_SHOTS = 7;

/**
 * The centrepiece. A particle system with one job: make the boundary something
 * you watch work rather than something you read about. Commands launch from the
 * host side; the ones that reach past the workspace shatter against the wall,
 * the ordinary ones pass through and settle into /workspace.
 *
 * Everything is drawn from CSS custom properties, so the canvas and the rest of
 * the page cannot drift apart.
 */
export const ContainmentCanvas = forwardRef<CanvasHandle, { className?: string }>(
  function ContainmentCanvas({ className }, ref) {
    const canvasRef = useRef<HTMLCanvasElement | null>(null);
    const shots = useRef<Shot[]>([]);
    const shards = useRef<Shard[]>([]);
    const ripples = useRef<Ripple[]>([]);
    const motes = useRef<Mote[]>([]);
    const size = useRef({ w: 0, h: 0 });
    const reduced = useRef(false);

    const launch = useCallback((label: string, passes: boolean) => {
      const { w, h } = size.current;
      if (!w) return;
      const y = h * (0.24 + Math.random() * 0.52);
      const speed = reduced.current ? 26 : 3.1 + Math.random() * 0.9;
      shots.current.push({
        x: w * 0.06,
        y,
        vx: speed,
        vy: (Math.random() - 0.5) * 0.35,
        label,
        passes,
        state: 0,
        life: 0,
      });
      if (shots.current.length > MAX_SHOTS) shots.current.shift();
    }, []);

    useImperativeHandle(ref, () => ({ launch }), [launch]);

    useEffect(() => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;

      const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
      reduced.current = mq.matches;
      const onMq = () => (reduced.current = mq.matches);
      mq.addEventListener("change", onMq);

      let raf = 0;
      let dpr = 1;

      const resize = () => {
        const rect = canvas.getBoundingClientRect();
        dpr = Math.min(window.devicePixelRatio || 1, 2);
        canvas.width = Math.round(rect.width * dpr);
        canvas.height = Math.round(rect.height * dpr);
        size.current = { w: rect.width, h: rect.height };
        // Ambient motes on the exposed side: the host is never quiet.
        motes.current = Array.from({ length: 26 }, () => ({
          x: Math.random() * rect.width * 0.46,
          y: Math.random() * rect.height,
          vx: (Math.random() - 0.5) * 0.16,
          vy: (Math.random() - 0.5) * 0.16,
          r: 0.7 + Math.random() * 1.5,
        }));
      };
      resize();
      const ro = new ResizeObserver(resize);
      ro.observe(canvas);

      const css = (name: string, fallback: string) =>
        getComputedStyle(canvas).getPropertyValue(name).trim() || fallback;

      const draw = () => {
        const { w, h } = size.current;
        const contained = css("--contained", "#059669");
        const exposed = css("--exposed", "#e11d48");
        const grid = css("--grid-line", "#ededf0");
        const border = css("--border", "#e7e7ea");
        const muted = css("--muted-foreground", "#70707a");
        const fg = css("--foreground", "#0b0b0d");
        const wallX = w * 0.54;

        ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
        ctx.clearRect(0, 0, w, h);

        // --- host side: dotted, restless ---------------------------------
        ctx.fillStyle = grid;
        for (let gx = 12; gx < wallX - 14; gx += 20)
          for (let gy = 12; gy < h - 8; gy += 20) {
            ctx.beginPath();
            ctx.arc(gx, gy, 0.8, 0, Math.PI * 2);
            ctx.fill();
          }

        ctx.fillStyle = muted;
        ctx.globalAlpha = 0.35;
        for (const m of motes.current) {
          if (!reduced.current) {
            m.x += m.vx;
            m.y += m.vy;
            if (m.x < 4 || m.x > wallX - 18) m.vx *= -1;
            if (m.y < 4 || m.y > h - 4) m.vy *= -1;
          }
          ctx.beginPath();
          ctx.arc(m.x, m.y, m.r, 0, Math.PI * 2);
          ctx.fill();
        }
        ctx.globalAlpha = 1;

        // --- workspace side: calm, tinted ---------------------------------
        const tint = ctx.createLinearGradient(wallX, 0, w, 0);
        tint.addColorStop(0, withAlpha(contained, 0.1));
        tint.addColorStop(1, withAlpha(contained, 0.02));
        ctx.fillStyle = tint;
        roundRect(ctx, wallX + 16, 10, Math.max(0, w - wallX - 26), h - 20, 12);
        ctx.fill();
        ctx.strokeStyle = withAlpha(contained, 0.32);
        ctx.lineWidth = 1;
        roundRect(ctx, wallX + 16, 10, Math.max(0, w - wallX - 26), h - 20, 12);
        ctx.stroke();

        // --- the wall ------------------------------------------------------
        const wall = ctx.createLinearGradient(wallX - 8, 0, wallX + 8, 0);
        wall.addColorStop(0, withAlpha(contained, 0.06));
        wall.addColorStop(0.5, withAlpha(contained, 0.3));
        wall.addColorStop(1, withAlpha(contained, 0.06));
        ctx.fillStyle = wall;
        ctx.fillRect(wallX - 7, 6, 14, h - 12);
        ctx.strokeStyle = contained;
        ctx.globalAlpha = 0.85;
        ctx.lineWidth = 1.6;
        ctx.beginPath();
        ctx.moveTo(wallX, 6);
        ctx.lineTo(wallX, h - 6);
        ctx.stroke();
        ctx.globalAlpha = 1;

        // the one opening: /workspace passes through here
        ctx.strokeStyle = css("--background", "#fff");
        ctx.lineWidth = 5;
        ctx.beginPath();
        ctx.moveTo(wallX, h * 0.5 - 16);
        ctx.lineTo(wallX, h * 0.5 + 16);
        ctx.stroke();
        ctx.strokeStyle = withAlpha(contained, 0.5);
        ctx.lineWidth = 1.2;
        ctx.setLineDash([3, 3]);
        ctx.beginPath();
        ctx.moveTo(wallX, h * 0.5 - 16);
        ctx.lineTo(wallX, h * 0.5 + 16);
        ctx.stroke();
        ctx.setLineDash([]);

        // --- shots ---------------------------------------------------------
        ctx.font = `500 11px ${css("--font-mono", "ui-monospace")}, ui-monospace, monospace`;
        ctx.textBaseline = "middle";

        for (const s of shots.current) {
          s.life += 1;
          if (s.state === 0) {
            s.x += s.vx;
            s.y += s.vy;
            if (s.x >= wallX - 3) {
              if (s.passes) {
                s.y += (h * 0.5 - s.y) * 0.35;
                s.state = 1;
                s.vx = 1.5;
                ripples.current.push({ x: wallX, y: h * 0.5, t: 0, passes: true });
              } else {
                s.state = 1;
                s.x = wallX - 3;
                ripples.current.push({ x: wallX, y: s.y, t: 0, passes: false });
                if (!reduced.current)
                  for (let i = 0; i < 22; i++) {
                    const a = Math.PI * (0.5 + Math.random());
                    const sp = 0.9 + Math.random() * 3.4;
                    shards.current.push({
                      x: wallX - 4,
                      y: s.y,
                      vx: Math.cos(a) * sp,
                      vy: Math.sin(a) * sp * 0.8,
                      r: 0.8 + Math.random() * 1.8,
                      life: 0,
                    });
                  }
              }
            }
          } else if (s.passes) {
            s.x += s.vx;
            s.vx *= 0.955;
          }

          const alpha = s.state === 1 && !s.passes ? Math.max(0, 1 - (s.life - 40) / 45) : 1;
          if (alpha <= 0) continue;

          const color = s.state === 1 ? (s.passes ? contained : exposed) : fg;
          ctx.globalAlpha = alpha;

          // the trail
          ctx.strokeStyle = withAlpha(color, 0.22);
          ctx.lineWidth = 1.4;
          ctx.beginPath();
          ctx.moveTo(Math.max(6, s.x - 34), s.y);
          ctx.lineTo(s.x, s.y);
          ctx.stroke();

          ctx.fillStyle = color;
          ctx.beginPath();
          ctx.arc(s.x, s.y, 3.2, 0, Math.PI * 2);
          ctx.fill();

          // the label rides just behind the head
          const text = s.label.length > 30 ? `${s.label.slice(0, 29)}…` : s.label;
          const tw = ctx.measureText(text).width;
          const lx = Math.min(Math.max(8, s.x - tw - 14), Math.max(8, wallX - tw - 16));
          ctx.fillStyle = css("--background", "#fff");
          ctx.globalAlpha = alpha * 0.9;
          roundRect(ctx, lx - 6, s.y - 9, tw + 12, 18, 5);
          ctx.fill();
          ctx.strokeStyle = withAlpha(s.state === 1 ? color : border, 0.7);
          ctx.lineWidth = 1;
          roundRect(ctx, lx - 6, s.y - 9, tw + 12, 18, 5);
          ctx.stroke();
          ctx.globalAlpha = alpha;
          ctx.fillStyle = s.state === 1 ? color : muted;
          ctx.fillText(text, lx, s.y + 0.5);
          ctx.globalAlpha = 1;
        }
        shots.current = shots.current.filter(
          (s) => s.life < 120 && s.x < size.current.w + 60,
        );

        // --- shards --------------------------------------------------------
        for (const sh of shards.current) {
          sh.life += 1;
          sh.x += sh.vx;
          sh.y += sh.vy;
          sh.vy += 0.045;
          sh.vx *= 0.985;
          ctx.globalAlpha = Math.max(0, 1 - sh.life / 55);
          ctx.fillStyle = exposed;
          ctx.beginPath();
          ctx.arc(sh.x, sh.y, sh.r, 0, Math.PI * 2);
          ctx.fill();
        }
        ctx.globalAlpha = 1;
        shards.current = shards.current.filter((s) => s.life < 55);

        // --- impact rings ---------------------------------------------------
        for (const r of ripples.current) {
          r.t += 1;
          const p = r.t / 34;
          ctx.globalAlpha = Math.max(0, 0.55 * (1 - p));
          ctx.strokeStyle = r.passes ? contained : exposed;
          ctx.lineWidth = 1.6;
          ctx.beginPath();
          ctx.arc(r.x, r.y, 4 + p * 26, 0, Math.PI * 2);
          ctx.stroke();
        }
        ctx.globalAlpha = 1;
        ripples.current = ripples.current.filter((r) => r.t < 34);

        raf = requestAnimationFrame(draw);
      };

      raf = requestAnimationFrame(draw);
      return () => {
        cancelAnimationFrame(raf);
        ro.disconnect();
        mq.removeEventListener("change", onMq);
      };
    }, []);

    return (
      <canvas
        ref={canvasRef}
        role="img"
        aria-label="Commands launched from the host side of a boundary: the ones reaching past the project shatter against it, ordinary project work passes through into /workspace."
        className={cn("h-full w-full", className)}
      />
    );
  },
);

function roundRect(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

/** Accepts the hex values the theme uses; falls back to the colour untouched. */
function withAlpha(color: string, alpha: number) {
  const m = /^#([0-9a-f]{6})$/i.exec(color.trim());
  if (!m) return color;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`;
}
