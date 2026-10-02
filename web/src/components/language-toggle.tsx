import Link from "next/link";
import { PYTHON_SDK_PATH, SDK_PATH } from "@/lib/site";
import { cn } from "@/lib/utils";

/**
 * Switch between the two clients.
 *
 * Links rather than client-side state, because these are two static routes
 * rather than two tabs of one: each keeps its own URL, so a link somebody sends
 * lands on the language they were reading, and neither page depends on
 * JavaScript to show its own content.
 *
 * The inactive side is a link and the active side is not — a control that
 * navigates to the page you are already on reads as broken the first time
 * somebody clicks it.
 */
export function LanguageToggle({ active }: { active: "typescript" | "python" }) {
  const options = [
    { id: "typescript", label: "TypeScript", href: SDK_PATH },
    { id: "python", label: "Python", href: PYTHON_SDK_PATH },
  ] as const;

  return (
    <div
      role="group"
      aria-label="Choose a client"
      className="inline-flex rounded-full border bg-card p-1"
    >
      {options.map((option) => {
        const current = option.id === active;
        const shape = "rounded-full px-4 py-1.5 text-[0.8rem] transition-colors";
        return current ? (
          <span
            key={option.id}
            aria-current="page"
            className={cn(shape, "bg-foreground font-medium text-background")}
          >
            {option.label}
          </span>
        ) : (
          <Link
            key={option.id}
            href={option.href}
            className={cn(shape, "text-muted-foreground hover:text-foreground")}
          >
            {option.label}
          </Link>
        );
      })}
    </div>
  );
}
