import { ShieldCheck } from "lucide-react";
import { PROFILE_MATRIX } from "@/lib/deploy";

/**
 * dev vs prod, as a plain side-by-side.
 *
 * This reads the same PROFILE_MATRIX the deployment section walks you through,
 * rather than restating it — two tables that could disagree about what a profile
 * does would be worse than one table in the wrong place. The difference is the
 * framing: down there it is scaffolding for a set of steps and only the column
 * you selected is lit; here both columns are equal, because a reader in the
 * tutorial has not chosen yet and the question they have is "which one am I".
 *
 * Static, so it renders on the server — there is nothing to interact with, and
 * shipping a client bundle for a table would be a cost with no return.
 */
export function ProfileDiff() {
  return (
    <div className="flex flex-col gap-3">
      <div className="overflow-hidden rounded-2xl border bg-card">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[40rem] border-collapse text-left">
            <thead>
              <tr className="border-b">
                <th className="px-4 py-3 text-[0.8rem] font-medium sm:px-5">
                  Within the same host boundary
                </th>
                <th className="px-4 py-3 text-[0.8rem] font-medium">
                  Local development
                  <span className="mt-0.5 block font-mono text-[0.62rem] font-normal text-muted-foreground">
                    --profile dev · default
                  </span>
                </th>
                <th className="px-4 py-3 text-[0.8rem] font-medium">
                  Production
                  <span className="mt-0.5 block font-mono text-[0.62rem] font-normal text-muted-foreground">
                    --profile prod
                  </span>
                </th>
              </tr>
            </thead>
            <tbody>
              {PROFILE_MATRIX.map((row) => (
                <tr key={row.setting} className="border-b last:border-0">
                  <td className="px-4 py-3 align-top sm:px-5">
                    <span className="block text-[0.8rem]">{row.setting}</span>
                    {row.note ? (
                      <span className="mt-1 block max-w-md text-[0.7rem] leading-relaxed text-muted-foreground">
                        {row.note}
                      </span>
                    ) : null}
                  </td>
                  <td className="px-4 py-3 align-top text-[0.78rem] leading-relaxed text-muted-foreground">
                    {row.dev}
                  </td>
                  <td className="px-4 py-3 align-top text-[0.78rem] leading-relaxed text-muted-foreground">
                    {row.prod}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="flex items-start gap-2.5 rounded-xl border border-contained-line bg-contained-soft/40 px-4 py-3">
        <ShieldCheck className="mt-0.5 size-4 shrink-0 text-contained" />
        <p className="text-[0.8rem] leading-relaxed text-muted-foreground">
          <span className="font-medium text-foreground">
            Neither of these is the insecure one.
          </span>{" "}
          Nothing in the host boundary is on this table, because no profile moves it: the three
          mount refusals, the privileged keys a project file may not set, the reserved environment
          names and the hardened root phase hold identically in both. What differs is what each
          optimises within that, plus one thing of kind — a control this host cannot provide is a
          warning under dev and a refusal under prod.{" "}
          <a href="#deploy" className="underline underline-offset-4 hover:text-foreground">
            The deployment section walks each one through, step by step.
          </a>
        </p>
      </div>
    </div>
  );
}
