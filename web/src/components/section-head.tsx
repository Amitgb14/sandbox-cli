import { cn } from "@/lib/utils";

export function SectionHead({
  eyebrow,
  title,
  lead,
  center,
  className,
}: {
  eyebrow: string;
  title: React.ReactNode;
  lead?: React.ReactNode;
  center?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "mb-10 flex max-w-3xl flex-col gap-3.5",
        center && "mx-auto items-center text-center",
        className,
      )}
    >
      <span className="eyebrow">
        <span className="h-px w-6 bg-current opacity-50" />
        {eyebrow}
      </span>
      <h2 className="text-[1.85rem] leading-[1.15] font-semibold tracking-[-0.028em] text-balance sm:text-[2.25rem]">
        {title}
      </h2>
      {lead ? (
        <p className="text-[1.02rem] leading-relaxed text-pretty text-muted-foreground">{lead}</p>
      ) : null}
    </div>
  );
}

/**
 * Alternating bands. `tinted` sections are full-bleed — the tint and its rules
 * run the whole width of the viewport while the content stays in the same
 * measure as everything else.
 */
export function Section({
  id,
  tinted,
  className,
  children,
}: {
  id?: string;
  tinted?: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section id={id} className={cn("w-full", tinted && "border-y bg-surface")}>
      <div
        className={cn(
          "mx-auto w-full max-w-6xl px-5 py-16 sm:px-6 lg:py-20",
          className,
        )}
      >
        {children}
      </div>
    </section>
  );
}
