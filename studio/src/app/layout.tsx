import type { Metadata, Viewport } from "next";
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import { Providers } from "@/components/providers";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Sandbox Studio",
    template: "%s · Sandbox Studio",
  },
  description:
    "A visual control plane for sandbox-cli — every agent run, the boundary it ran inside, and what it changed.",
};

/**
 * Rendered per request, so the two values below are read when the container
 * *runs* rather than when the image was built.
 *
 * Without this Next prerenders the layout at build time — the build log says so,
 * `○ (Static)` — and `process.env` is read once, on a machine that knows neither
 * this user's port nor their token. The result is an image whose runtime
 * configuration silently does nothing: the page renders, the script tag is
 * simply absent, and every screen falls back to the baked default.
 *
 * It costs a control plane nothing. Nothing here fetches on the server; the
 * pages are client components talking to a daemon on another origin, so a
 * "static" render was only ever saving the cost of emitting the same shell.
 */
export const dynamic = "force-dynamic";

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#0a0a0c" },
    { media: "(prefers-color-scheme: light)", color: "#fafafa" },
  ],
};

/**
 * A string, encoded so it is safe *inside a `<script>` element*.
 *
 * `JSON.stringify` alone is not that, which is easy to believe it is: it escapes
 * quotes and control characters for a JavaScript *parser*, while the HTML parser
 * gets there first and ends the block at the literal text `</script`, wherever it
 * appears — inside a string literal included. `<!--` opens a comment on the same
 * terms. Escaping every `<` as its `<` form removes both, and changes
 * nothing about the value the browser ends up with.
 *
 * Today these values come from the operator's own environment, so this is
 * hygiene rather than a hole. It is written down because the alternative is a
 * comment claiming an encoding is a safety measure when it is not.
 */
function scriptSafe(value: string): string {
  return JSON.stringify(value).replace(/</g, "\\u003c");
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  // Where the daemon is, handed to the browser at *request* time.
  //
  // The published image cannot bake this in: `NEXT_PUBLIC_*` is inlined when the
  // image is built, and which port a user's daemon ended up on is not knowable
  // then. So the container is told with `-e SANDBOX_API_URL=…` and passes it on
  // here — see `apiBase()`, which prefers this over the baked value for exactly
  // that reason.
  //
  // Emitted only when it is set, so `npm run dev` and any self-built deployment
  // keep the build-time value and this script does not appear at all.
  //
  // The token rides along for the same reason: a script that starts both halves
  // knows the token it generated, and making someone copy it out of a terminal
  // into a settings field is friction with nothing on the other side of it.
  // Same-origin script content, which is what the localStorage copy already is.
  const apiUrl = process.env.SANDBOX_API_URL;
  const apiToken = process.env.SANDBOX_STUDIO_TOKEN;
  const boot = [
    apiUrl ? `window.__SANDBOX_API__=${scriptSafe(apiUrl)};` : "",
    apiToken ? `window.__SANDBOX_TOKEN__=${scriptSafe(apiToken)};` : "",
  ].join("");
  return (
    // `className="dark"` is the designed default, and next-themes takes over
    // from there. suppressHydrationWarning is required: the theme script rewrites
    // this attribute before React hydrates, on purpose.
    <html lang="en" className="dark" suppressHydrationWarning>
      {boot ? (
        <head>
          <script dangerouslySetInnerHTML={{ __html: boot }} />
        </head>
      ) : null}
      <body
        className={`${GeistSans.variable} ${GeistMono.variable} font-sans antialiased`}
      >
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
