import Image from "next/image";

/**
 * A screenshot of Studio against a real sandboxd, in the theme the site is
 * showing: public/studio/<name>-light.png and -dark.png, one hidden by the
 * `dark:` variant. Each opens full size in its own theme.
 */
export function StudioShot({ name, title, priority }: { name: string; title: string; priority?: boolean }) {
  const frame = "overflow-hidden rounded-xl border bg-card shadow-sm transition-shadow hover:shadow-md";
  return (
    <>
      {(["light", "dark"] as const).map((theme) => (
        <a
          key={theme}
          href={`/studio/${name}-${theme}.png`}
          className={`${frame} ${theme === "light" ? "block dark:hidden" : "hidden dark:block"}`}
        >
          <Image
            src={`/studio/${name}-${theme}.png`}
            width={2160}
            height={1350}
            alt={`Sandbox Studio, the ${title} screen`}
            priority={priority}
            className="h-auto w-full"
          />
        </a>
      ))}
    </>
  );
}
