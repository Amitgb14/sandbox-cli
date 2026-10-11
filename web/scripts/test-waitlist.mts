// Tests for api/waitlist.ts, with a stand-in for the email service: nothing is
// sent. Run with `npm run test:waitlist` (Node 22.18+ runs TypeScript as is).
import assert from "node:assert/strict";
import { test } from "node:test";
import { handle, parseSignup } from "../api/waitlist.ts";

const ENV = { RESEND_API_KEY: "re_test_key" };
const GOOD = { firstName: "Ada", lastName: "Lovelace", email: "ada@example.com" };

function post(body: unknown, headers: Record<string, string> = {}) {
  return new Request("https://sandbox-cli.example/api/waitlist", {
    method: "POST",
    headers: { "content-type": "application/json", host: "sandbox-cli.example", ...headers },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

/** A stand-in for the email service that records what it was asked to send. */
function recorder(status = 200) {
  const sent: { url: string; init: RequestInit }[] = [];
  const send = (async (url: string, init: RequestInit) => {
    sent.push({ url, init });
    return new Response(status === 200 ? '{"id":"e1"}' : '{"message":"domain not verified"}', { status });
  }) as unknown as typeof fetch;
  return { sent, send };
}

test("a sign-up is mailed to the waitlist, as plain text, with the key", async () => {
  const { sent, send } = recorder();
  const res = await handle(post(GOOD, { origin: "https://sandbox-cli.example" }), ENV, send);
  assert.equal(res.status, 200);
  assert.equal(sent.length, 1);
  assert.equal(sent[0].url, "https://api.resend.com/emails");
  assert.equal((sent[0].init.headers as Record<string, string>).Authorization, "Bearer re_test_key");
  const msg = JSON.parse(sent[0].init.body as string);
  assert.deepEqual(msg.to, ["waitlist@sandbox-cli.com"]);
  assert.equal(msg.from, "sandbox-cli waitlist <waitlist@sandbox-cli.com>");
  assert.equal(msg.reply_to, "ada@example.com");
  assert.equal(msg.subject, "Waitlist: Ada Lovelace");
  assert.match(msg.text, /First name: Ada\nLast name:  Lovelace\nEmail:      ada@example.com/);
  assert.equal(msg.html, undefined);
  // The key is in the request to the service and nowhere in the answer.
  assert.doesNotMatch(await res.text(), /re_test_key/);
});

test("WAITLIST_TO and WAITLIST_FROM override the addresses", async () => {
  const { sent, send } = recorder();
  await handle(post(GOOD), { ...ENV, WAITLIST_TO: "list@example.org", WAITLIST_FROM: "W <w@example.org>" }, send);
  const msg = JSON.parse(sent[0].init.body as string);
  assert.deepEqual(msg.to, ["list@example.org"]);
  assert.equal(msg.from, "W <w@example.org>");
});

test("each field is required, bounded and free of control characters", () => {
  for (const [body, want] of [
    [{ ...GOOD, firstName: "  " }, "first name is required"],
    [{ ...GOOD, lastName: undefined }, "last name is required"],
    [{ ...GOOD, email: "" }, "email is required"],
    [{ ...GOOD, firstName: "x".repeat(101) }, "longer than 100"],
    [{ ...GOOD, lastName: "Smith\r\nBcc: victim@example.com" }, "cannot have"],
    [{ ...GOOD, email: "not-an-address" }, "does not look like"],
    [{ ...GOOD, email: "a b@example.com" }, "does not look like"],
    [[], "is required"],
    [null, "expected a JSON object"],
  ] as const) {
    const got = parseSignup(body);
    assert.ok("error" in got, `accepted ${JSON.stringify(body)}`);
    assert.match(got.error, new RegExp(want));
  }
  assert.deepEqual(parseSignup({ firstName: " Ada ", lastName: "Lovelace", email: "ada@example.com " }), GOOD);
});

test("bad requests are refused before anything is sent", async () => {
  const { sent, send } = recorder();
  const cases: [Request, number][] = [
    [new Request("https://sandbox-cli.example/api/waitlist"), 405],
    [post(GOOD, { origin: "https://elsewhere.example" }), 403],
    [post(GOOD, { origin: "null" }), 403],
    [post("first=Ada", { "content-type": "application/x-www-form-urlencoded" }), 415],
    [post("{not json"), 400],
    [post({ ...GOOD, note: "x".repeat(5000) }), 413],
    [post({ ...GOOD, email: "nope" }), 400],
  ];
  for (const [req, status] of cases) {
    assert.equal((await handle(req, ENV, send)).status, status, `${req.method} ${[...req.headers]}`);
  }
  assert.equal(sent.length, 0);
});

test("the hidden field answers ok and sends nothing", async () => {
  const { sent, send } = recorder();
  const res = await handle(post({ ...GOOD, website: "http://spam.example" }), ENV, send);
  assert.equal(res.status, 200);
  assert.equal(sent.length, 0);
});

test("without a key it is 503, and a refused send is 502 without the service's words", async () => {
  const { sent, send } = recorder();
  assert.equal((await handle(post(GOOD), {}, send)).status, 503);
  assert.equal(sent.length, 0);

  const failing = recorder(403);
  const res = await handle(post(GOOD), ENV, failing.send);
  assert.equal(res.status, 502);
  assert.doesNotMatch(await res.text(), /domain not verified/);
});
