"use client";

import { Moon, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toggleTheme } from "@/lib/theme";

/**
 * Both icons are rendered and CSS picks one, so the button needs no state: the
 * server cannot know the theme, and state read on the client would mismatch
 * the static HTML on hydration.
 */
export function ThemeToggle() {
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      onClick={toggleTheme}
      aria-label="Toggle dark mode"
      title="Toggle dark mode"
      className="text-muted-foreground hover:text-foreground"
    >
      <Sun className="hidden dark:block" />
      <Moon className="dark:hidden" />
    </Button>
  );
}
