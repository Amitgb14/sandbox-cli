import type { Metadata } from "next";
import { Geist_Mono, Inter } from "next/font/google";
import { TooltipProvider } from "@/components/ui/tooltip";
import { REPO_URL } from "@/lib/site";
import "./globals.css";

/** Body + display. One family, used at very different sizes. */
const inter = Inter({
  variable: "--font-inter",
  subsets: ["latin"],
  display: "swap",
});

/**
 * The terminal voice. The subject is a CLI, so mono carries most of the
 * evidence on this page — commands, argv, paths, gauges.
 */
const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
  display: "swap",
});

const TITLE = "sandbox-cli — isolated microVM sandboxes, on your Mac, your server or the cloud";
const DESCRIPTION =
  "A disposable microVM for any command — a test suite, or Claude Code and Codex at full autonomy — behind one API, on your Mac, a Linux machine you control, or the cloud. A sandbox starts in its own home directory; nothing of yours is mounted; egress is an allowlist enforced outside the guest.";

export const metadata: Metadata = {
  metadataBase: new URL("https://github.com/Amitgb14/sandbox-cli"),
  title: TITLE,
  description: DESCRIPTION,
  keywords: [
    "sandbox",
    "AI coding agent",
    "Claude Code",
    "Codex",
    "microVM",
    "Firecracker",
    "sandbox API",
    "isolation",
    "secure development",
    "prompt injection",
    "devsecops",
  ],
  authors: [{ name: "Amitgb14", url: REPO_URL }],
  openGraph: {
    title: TITLE,
    description: DESCRIPTION,
    url: REPO_URL,
    siteName: "sandbox-cli",
    type: "website",
  },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" className={`${inter.variable} ${geistMono.variable} h-full`}>
      <body className="flex min-h-full flex-col antialiased">
        <TooltipProvider delay={120}>{children}</TooltipProvider>
      </body>
    </html>
  );
}
