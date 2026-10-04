import type { Metadata, Viewport } from "next";
// Inter for text, bundled with the UI rather than fetched from a font service:
// Studio is served by sandbox-cli on the user's own machine, often offline.
import "@fontsource-variable/inter";
import { GeistMono } from "geist/font/mono";
import { Providers } from "@/components/providers";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Sandbox Studio",
    template: "%s · Sandbox Studio",
  },
  description:
    "The browser view of your sandboxes: what is running, which agents are waiting for you, and what each sandbox did.",
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#000000" },
    { media: "(prefers-color-scheme: light)", color: "#ffffff" },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    // `className="dark"` is the designed default, and next-themes takes over
    // from there. suppressHydrationWarning is required: the theme script rewrites
    // this attribute before React hydrates, on purpose.
    <html lang="en" className="dark" suppressHydrationWarning>
      <body className={`${GeistMono.variable} font-sans antialiased`}>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
