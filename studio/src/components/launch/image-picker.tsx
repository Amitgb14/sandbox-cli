"use client";

import { useState } from "react";
import { Check, ChevronsUpDown, HardDrive, Monitor, Package, Plus, Star } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator } from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

const DESKTOP = /sandbox-base(?=:|@|$)/;

function short(image: string) {
  return image.replace(/^.*\//, "");
}

/**
 * The image a sandbox starts from, chosen from what is real — the images this
 * machine has already built (they start without a pull), those its sandboxes
 * run, and the desktop image beside each base one — or typed. Empty is the
 * server's default. sandboxd's policy decides whether any of them may run.
 */
export function ImagePicker({
  value,
  onChange,
  built,
  inUse,
}: {
  value: string;
  onChange: (image: string) => void;
  /** Images whose root disk this machine has already built (GET /v1/node). */
  built: string[];
  /** Images the endpoint's sandboxes run. */
  inUse: string[];
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const all = [...built, ...inUse];
  const desktop = [...new Set(all.filter((i) => DESKTOP.test(i)).map((i) => i.replace(DESKTOP, "sandbox-desktop")))];
  const builtSet = new Set(built);
  const groups: { title: string; icon: React.ComponentType<{ className?: string }>; items: string[] }[] = [
    { title: "Desktop", icon: Monitor, items: desktop.filter((i) => !builtSet.has(i)) },
    { title: "Built on this machine", icon: HardDrive, items: [...builtSet] },
    { title: "Used by sandboxes here", icon: Package, items: [...new Set(inUse)].filter((i) => !builtSet.has(i) && !desktop.includes(i)) },
  ].filter((g) => g.items.length);
  const typed = query.trim();
  const choose = (v: string) => {
    onChange(v);
    setOpen(false);
    setQuery("");
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" role="combobox" aria-expanded={open} aria-label="Image" className="h-auto w-full justify-between py-2 font-normal">
          <span className="flex min-w-0 flex-col items-start gap-0.5 text-left">
            <span className={cn("truncate text-sm", value && "font-mono text-[13px]")}>{value ? short(value) : "The server's default image"}</span>
            <span className="truncate text-[11px] text-muted-foreground">
              {value ? value : "what sandboxd runs when a request names none"}
              {value && builtSet.has(value) ? " · built here, starts without a pull" : ""}
            </span>
          </span>
          <ChevronsUpDown className="size-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-(--radix-popover-trigger-width) min-w-80 p-0" align="start">
        <Command>
          <CommandInput placeholder="Search, or type any image…" value={query} onValueChange={setQuery} />
          <CommandList>
            <CommandEmpty>No suggestion matches.</CommandEmpty>
            {typed && !all.includes(typed) && !desktop.includes(typed) ? (
              <>
                <CommandGroup>
                  <CommandItem value={`use ${typed}`} onSelect={() => choose(typed)}>
                    <Plus className="size-4" />
                    Use <span className="font-mono">{typed}</span>
                  </CommandItem>
                </CommandGroup>
                <CommandSeparator />
              </>
            ) : null}
            <CommandGroup>
              <CommandItem value="the server's default image" onSelect={() => choose("")}>
                <Star className="size-4" />
                <span className="flex-1">The server&apos;s default image</span>
                {!value ? <Check className="size-4" /> : null}
              </CommandItem>
            </CommandGroup>
            {groups.map((g) => (
              <CommandGroup key={g.title} heading={g.title}>
                {g.items.map((i) => (
                  <CommandItem key={i} value={i} onSelect={() => choose(i)}>
                    <g.icon className="size-4" />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate font-mono text-[13px]">{short(i)}</span>
                      <span className="truncate text-[11px] text-muted-foreground">{i}</span>
                    </span>
                    {value === i ? <Check className="size-4" /> : null}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
