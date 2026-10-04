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
    "The browser view of your sandboxes: what is running, what came back, and what each sandbox did.",
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#09090b" },
    { media: "(prefers-color-scheme: light)", color: "#f7f7f8" },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    // `className="dark"` is the designed default, and next-themes takes over
    // from there. suppressHydrationWarning is required: the theme script rewrites
    // this attribute before React hydrates, on purpose.
    <html lang="en" className="dark" suppressHydrationWarning>
      <body className={`${GeistSans.variable} ${GeistMono.variable} font-sans antialiased`}>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
