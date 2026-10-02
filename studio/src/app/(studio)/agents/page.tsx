"use client";

import Link from "next/link";
import { PageHeader } from "@/components/common/page-header";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAgents } from "@/lib/api/queries";

/**
 * The twelve agents. Unattended means a verified headless mode — the only
 * agents a fleet, a fallback or an unattended launch may use, because one that
 * stops to ask with nobody there does not fail, it hangs.
 */
export default function AgentsPage() {
  const { data } = useAgents();
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Agents"
        description="A login is copied into each sandbox an agent runs in, and back out when it ends. Log in once with sandbox-cli agent <name>, or start a console here."
      />
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Agent</TableHead>
            <TableHead>Unattended</TableHead>
            <TableHead>Login</TableHead>
            <TableHead className="text-right" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data ?? []).map((a) => (
            <TableRow key={a.name}>
              <TableCell className="font-mono text-sm">{a.name}</TableCell>
              <TableCell>{a.unattended ? <Badge variant="outline" className="border-contained/40 text-contained">verified headless</Badge> : <span className="text-muted-foreground">interactive only</span>}</TableCell>
              <TableCell className="text-sm">{a.login === "saved" ? "saved" : a.login === "not kept" ? <span className="text-muted-foreground">not kept between runs</span> : <span className="text-muted-foreground">not yet</span>}</TableCell>
              <TableCell className="text-right">
                <Link href="/launch" className="text-sm text-muted-foreground hover:text-foreground">launch →</Link>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
