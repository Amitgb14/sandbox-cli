"use client";

import { useCallback, useEffect, useRef } from "react";

export type ShotKind = "blocked" | "allowed";

export interface CanvasHandle {
  fire: (label: string, kind: ShotKind) => void;
}

interface Projectile {
  x: number;
  y: number;
  vx: number;
  label: string;
  kind: ShotKind;
  alive: boolean;
  /** 0→1 fade used once it has passed the wall. */
  fade: number;
}

interface Shard {
  x: number;
  y: number;
  vx: number;
  vy: number;
  life: number;
  maxLife: number;
  size: number;
}

interface Drifter {
  x: number;
  y: number;
  vy: number;
  size: number;
  alpha: number;
}

function readVar(el: HTMLElement, name: string, fallback: string): string {
  const v = getComputedStyle(el).getPropertyValue(name).trim();
  return v || fallback;
}

/**
 * The containment boundary, drawn on canvas.
 *
 * Commands launch from the host side and travel toward the wall. Blocked ones
 * shatter against it; allowed ones pass through into /workspace. Ambient motes
 * drift on the host side so the exposed territory never looks inert.
 */
export function ContainmentCanvas({
  onImpact,
  className,
}: {
  onImpact?: (kind: ShotKind) => void;
  className?: string;
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const projectiles = useRef<Projectile[]>([]);
  const shards = useRef<Shard[]>([]);
  const drifters = useRef<Drifter[]>([]);
  const flash = useRef<{ v: number; kind: ShotKind }>({ v: 0, kind: "blocked" });
  const sizeRef = useRef<{ w: number; h: number }>({ w: 0, h: 0 });
  const rafRef = useRef<number>(0);
  const reduced = useRef(false);

  const fire = useCallback((label: string, kind: ShotKind) => {
    const { w, h } = sizeRef.current;
    if (!w) return;
    projectiles.current.push({
      x: w * 0.06,
      y: h / 2,
      vx: w * 0.006,
      label,
      kind,
      alive: true,
      fade: 1,
    });
  }, []);

  // Expose `fire` on the DOM node so the parent can drive it without a ref forward.
  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    (el as HTMLCanvasElement & { fire?: CanvasHandle["fire"] }).fire = fire;
  }, [fire]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    reduced.current = matchMedia("(prefers-reduced-motion: reduce)").matches;

    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.floor(rect.width * dpr);
      canvas.height = Math.floor(rect.height * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      sizeRef.current = { w: rect.width, h: rect.height };

      // Seed ambient motes across the host half.
      drifters.current = Array.from({ length: 26 }, (_, i) => ({
        x: Math.random() * rect.width * 0.44,
        y: Math.random() * rect.height,
        vy: 0.08 + ((i % 5) * 0.03),
        size: 1 + (i % 3) * 0.6,
        alpha: 0.12 + (i % 4) * 0.05,
      }));
    };

    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(canvas);

    const draw = () => {
      const { w, h } = sizeRef.current;
      if (!w || !h) {
        rafRef.current = requestAnimationFrame(draw);
        return;
      }

      const host = canvas as HTMLElement;
      const cContained = readVar(host, "--contained", "#2dd4be");
      const cExposed = readVar(host, "--exposed", "#ff6f5e");
      const cBorder = readVar(host, "--border", "#26303c");
      const cMuted = readVar(host, "--muted-foreground", "#8493a4");

      const wallX = w * 0.5;

      ctx.clearRect(0, 0, w, h);

      // --- host-side hatching -------------------------------------------
      ctx.save();
      ctx.beginPath();
      ctx.rect(0, 0, wallX, h);
      ctx.clip();
      ctx.strokeStyle = cExposed;
      ctx.globalAlpha = 0.07;
      ctx.lineWidth = 1;
      for (let x = -h; x < wallX + h; x += 13) {
        ctx.beginPath();
        ctx.moveTo(x, 0);
        ctx.lineTo(x + h, h);
        ctx.stroke();
      }
      ctx.restore();

      // --- sandbox-side tint --------------------------------------------
      ctx.save();
      ctx.globalAlpha = 0.05;
      ctx.fillStyle = cContained;
      ctx.fillRect(wallX, 0, w - wallX, h);
      ctx.restore();

      // --- ambient motes on the host side --------------------------------
      if (!reduced.current) {
        for (const d of drifters.current) {
          d.y += d.vy;
          if (d.y > h) {
            d.y = -4;
            d.x = Math.random() * w * 0.44;
          }
          ctx.save();
          ctx.globalAlpha = d.alpha;
          ctx.fillStyle = cExposed;
          ctx.beginPath();
          ctx.arc(d.x, d.y, d.size, 0, Math.PI * 2);
          ctx.fill();
          ctx.restore();
        }
      }

      // --- the wall -------------------------------------------------------
      const f = flash.current.v;
      const wallColor = f > 0.01 ? (flash.current.kind === "allowed" ? cContained : cExposed) : cBorder;
      ctx.save();
      if (f > 0.01) {
        ctx.shadowColor = wallColor;
        ctx.shadowBlur = 26 * f;
      }
      const grad = ctx.createLinearGradient(0, 0, 0, h);
      grad.addColorStop(0, "transparent");
      grad.addColorStop(0.12, wallColor);
      grad.addColorStop(0.88, wallColor);
      grad.addColorStop(1, "transparent");
      ctx.strokeStyle = grad;
      ctx.lineWidth = 2 + 1.5 * f;
      ctx.beginPath();
      ctx.moveTo(wallX, 0);
      ctx.lineTo(wallX, h);
      ctx.stroke();
      ctx.restore();
      flash.current.v *= 0.92;

      // --- side labels ----------------------------------------------------
      ctx.save();
      ctx.font = "500 10px var(--font-plex-mono), ui-monospace, monospace";
      ctx.fillStyle = cMuted;
      ctx.globalAlpha = 0.75;
      ctx.textBaseline = "top";
      ctx.fillText("HOST — EXPOSED", 12, 12);
      const rl = "SANDBOX — CONTAINED";
      ctx.fillText(rl, w - ctx.measureText(rl).width - 12, 12);
      ctx.restore();

      // --- projectiles ------------------------------------------------------
      for (const p of projectiles.current) {
        if (!p.alive) continue;

        if (reduced.current) {
          // Skip the travel entirely; resolve immediately.
          p.alive = false;
          flash.current = { v: 1, kind: p.kind };
          onImpact?.(p.kind);
          continue;
        }

        p.x += p.vx;

        const hitWall = p.x >= wallX - 4;
        if (hitWall && p.kind === "blocked") {
          p.alive = false;
          flash.current = { v: 1, kind: "blocked" };
          onImpact?.("blocked");
          // shatter
          for (let i = 0; i < 30; i++) {
            const a = Math.PI * (0.5 + Math.random());
            const sp = 1.2 + Math.random() * 3.4;
            shards.current.push({
              x: wallX - 3,
              y: p.y + (Math.random() - 0.5) * 14,
              vx: Math.cos(a) * sp,
              vy: Math.sin(a) * sp * 0.7,
              life: 0,
              maxLife: 34 + Math.random() * 26,
              size: 1 + Math.random() * 2,
            });
          }
          continue;
        }

        if (p.kind === "allowed" && p.x > wallX) {
          if (Math.abs(p.x - wallX) < p.vx * 1.5) {
            flash.current = { v: 0.85, kind: "allowed" };
            onImpact?.("allowed");
          }
          if (p.x > w * 0.82) p.fade -= 0.06;
          if (p.fade <= 0) {
            p.alive = false;
            continue;
          }
        }

        const color = p.kind === "allowed" ? cContained : cExposed;
        ctx.save();
        ctx.globalAlpha = Math.max(0, p.fade);

        // trail
        const tg = ctx.createLinearGradient(p.x - 54, 0, p.x, 0);
        tg.addColorStop(0, "transparent");
        tg.addColorStop(1, color);
        ctx.strokeStyle = tg;
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        ctx.moveTo(p.x - 54, p.y);
        ctx.lineTo(p.x, p.y);
        ctx.stroke();

        // head
        ctx.fillStyle = color;
        ctx.shadowColor = color;
        ctx.shadowBlur = 10;
        ctx.beginPath();
        ctx.arc(p.x, p.y, 3.2, 0, Math.PI * 2);
        ctx.fill();
        ctx.shadowBlur = 0;

        // label
        ctx.font = "500 11px var(--font-plex-mono), ui-monospace, monospace";
        ctx.fillStyle = color;
        ctx.textBaseline = "bottom";
        const text = p.label.length > 26 ? `${p.label.slice(0, 25)}…` : p.label;
        ctx.fillText(text, Math.max(6, p.x - ctx.measureText(text).width - 10), p.y - 8);
        ctx.restore();
      }
      projectiles.current = projectiles.current.filter((p) => p.alive);

      // --- shards -----------------------------------------------------------
      for (const s of shards.current) {
        s.life += 1;
        s.x += s.vx;
        s.y += s.vy;
        s.vy += 0.06; // gravity
        s.vx *= 0.985;
        const t = 1 - s.life / s.maxLife;
        if (t <= 0) continue;
        ctx.save();
        ctx.globalAlpha = t * 0.9;
        ctx.fillStyle = cExposed;
        ctx.beginPath();
        ctx.arc(s.x, s.y, s.size * t, 0, Math.PI * 2);
        ctx.fill();
        ctx.restore();
      }
      shards.current = shards.current.filter((s) => s.life < s.maxLife);

      rafRef.current = requestAnimationFrame(draw);
    };

    rafRef.current = requestAnimationFrame(draw);

    return () => {
      cancelAnimationFrame(rafRef.current);
      ro.disconnect();
    };
  }, [onImpact]);

  return <canvas ref={canvasRef} className={className} aria-hidden="true" />;
}
