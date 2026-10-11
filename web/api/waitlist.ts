/**
 * POST /api/waitlist: a sign-up from the home page's waitlist form, mailed to
 * the waitlist inbox.
 *
 * The site is a static export, so this is a Vercel function beside it rather
 * than a Next route handler (a static export serves GET only). It compiles to
 * a .js with `export`; api/package.json says "type": "module" so the platform
 * loads it as one. Without it the nearest package.json was web's, which says
 * nothing, so it was loaded as CommonJS and every request failed before the
 * handler ran. (A .mts file is not picked up as a function at all.) It runs on the
 * host, where the email service's API key is: RESEND_API_KEY, set in the
 * project's environment and never sent to the browser. On a host without
 * functions (GitHub Pages, a bucket) the form gets a 404 and says so.
 *
 * Everything in the request is untrusted. The fields are checked for length
 * and control characters (a newline in a name would otherwise reach the
 * subject line), the email is sent as plain text so no field is markup, and
 * the visitor's address only ever goes in reply_to, never in to or from. A
 * hidden field that people leave empty catches the simplest bots; there is no
 * rate limit, which a function with no state cannot keep.
 *
 *   RESEND_API_KEY   required; without it every sign-up is 503
 *   WAITLIST_TO      default waitlist@sandbox-cli.com
 *   WAITLIST_FROM    default "sandbox-cli waitlist <waitlist@sandbox-cli.com>";
 *                    its domain must be verified with the email service
 */

const SEND_URL = "https://api.resend.com/emails";
const DEFAULT_TO = "waitlist@sandbox-cli.com";
const DEFAULT_FROM = "sandbox-cli waitlist <waitlist@sandbox-cli.com>";

export type Signup = { firstName: string; lastName: string; email: string };

// One @, something on each side, a dot in the domain, no spaces: enough to
// catch a typo without refusing a real address.
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
// C0 controls, DEL and C1 controls: none belong in a name or an address.
const CONTROL_RE = /[\u0000-\u001f\u007f-\u009f]/;

/** The sign-up in body, or why it is not one. */
export function parseSignup(body: unknown): Signup | { error: string } {
  if (typeof body !== "object" || body === null) return { error: "expected a JSON object" };
  const b = body as Record<string, unknown>;
  const field = (k: string) => (typeof b[k] === "string" ? (b[k] as string).trim() : "");
  const firstName = field("firstName");
  const lastName = field("lastName");
  const email = field("email");
  for (const [name, value, max] of [
    ["first name", firstName, 100],
    ["last name", lastName, 100],
    ["email", email, 254],
  ] as const) {
    if (!value) return { error: `${name} is required` };
    if (value.length > max) return { error: `${name} is longer than ${max} characters` };
    if (CONTROL_RE.test(value)) return { error: `${name} has a character it cannot have` };
  }
  if (!EMAIL_RE.test(email)) return { error: "that does not look like an email address" };
  return { firstName, lastName, email };
}

/** The message the waitlist inbox receives. */
export function message(s: Signup, now: Date, env: Record<string, string | undefined>) {
  return {
    from: env.WAITLIST_FROM || DEFAULT_FROM,
    to: [env.WAITLIST_TO || DEFAULT_TO],
    reply_to: s.email,
    subject: `Waitlist: ${s.firstName} ${s.lastName}`,
    text: [
      "A new sign-up for the sandbox-cli waitlist.",
      "",
      `First name: ${s.firstName}`,
      `Last name:  ${s.lastName}`,
      `Email:      ${s.email}`,
      `Signed up:  ${now.toISOString()}`,
    ].join("\n"),
  };
}

const json = (status: number, body: unknown) => Response.json(body, { status });

// An Origin's host, or "" for one that is not a URL ("null", from a sandboxed
// frame or a file), which then matches no host.
function hostOf(origin: string) {
  try {
    return new URL(origin).host;
  } catch {
    return "";
  }
}

export async function handle(
  request: Request,
  env: Record<string, string | undefined> = process.env,
  send: typeof fetch = fetch,
): Promise<Response> {
  if (request.method !== "POST") return json(405, { error: "POST only" });

  // A browser sends Origin on a cross-site POST; a page elsewhere may not
  // sign people up from its visitors' browsers.
  const origin = request.headers.get("origin");
  const host = request.headers.get("host");
  if (origin && host && hostOf(origin) !== host) return json(403, { error: "cross-site request" });

  if (!(request.headers.get("content-type") || "").includes("application/json")) {
    return json(415, { error: "send JSON" });
  }
  const raw = await request.text();
  if (raw.length > 4096) return json(413, { error: "too large" });
  let body: unknown;
  try {
    body = JSON.parse(raw);
  } catch {
    return json(400, { error: "not JSON" });
  }

  // The hidden field: a person never fills it. Answer as if it worked, so a
  // bot learns nothing.
  if ((body as Record<string, unknown>)?.website) return json(200, { ok: true });

  const signup = parseSignup(body);
  if ("error" in signup) return json(400, signup);

  const key = env.RESEND_API_KEY;
  if (!key) {
    console.error("waitlist: RESEND_API_KEY is not set");
    return json(503, { error: "the waitlist is not taking sign-ups right now" });
  }
  const res = await send(SEND_URL, {
    method: "POST",
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    body: JSON.stringify(message(signup, new Date(), env)),
  });
  if (!res.ok) {
    // The service's answer goes to the log, not to the visitor.
    console.error(`waitlist: sending failed: ${res.status} ${(await res.text()).slice(0, 500)}`);
    return json(502, { error: "could not record the sign-up; please try again later" });
  }
  return json(200, { ok: true });
}

export function POST(request: Request) {
  return handle(request);
}
