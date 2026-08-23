"""Fetch a stock price with untrusted code, and let it reach exactly one host.

The point of this example is not the price. It is the two lines that decide what
the code can do:

    allow=["query1.finance.yahoo.com"]     # the only host it may reach
    ws.run(["python3", "-c", SOURCE])      # no shell, so nothing to quote

`allow` **adds** to the daemon's posture and can never loosen it — the same
tighten-only rule a project config gets. What that means in practice is worth
measuring rather than assuming, because it is not "permit these extras":

    without allow:  urlopen("https://example.com")  -> 200
    with    allow:  urlopen("https://example.com")  -> blocked

…on a daemon whose egress is otherwise **unrestricted**. Naming a host turns the
allowlist *on* for that run, and everything unnamed is then refused. So this
example is not asking for more reach than it had; it is giving up the rest of the
internet in exchange for one host. On a daemon already running an allowlist it
adds one domain, and on `mode: none` it changes nothing and the fetch fails,
which is the correct outcome rather than a surprise.

The code travels as an argv element, not through `sh -c`. Nothing parses it, so a
quote or a `$(...)` in the source is just text — the hazard that makes string
interpolation into a shell command a bad habit does not exist here.
"""

import json
import sys

from sandbox_cli import ApiError, Studio

SYMBOL = sys.argv[1] if len(sys.argv) > 1 else "TSLA"
QUOTE_HOST = "query1.finance.yahoo.com"

# Runs inside the container. Standard library only: the image ships python 3.11
# and no pip, so anything from PyPI would need a different image or a pip layer.
FETCH = f'''
import json, urllib.request
url = "https://{QUOTE_HOST}/v8/finance/chart/{SYMBOL}?interval=1d&range=1d"
req = urllib.request.Request(url, headers={{"User-Agent": "sandbox-cli-example"}})
with urllib.request.urlopen(req, timeout=20) as r:
    meta = json.load(r)["chart"]["result"][0]["meta"]
print(json.dumps({{
    "symbol": meta.get("symbol"),
    "price": meta.get("regularMarketPrice"),
    "currency": meta.get("currency"),
    "exchange": meta.get("fullExchangeName"),
}}))
'''


def main() -> int:
    studio = Studio.connect()
    repo = studio.project()               # the repository this script is standing in
    ws = repo.workspace(f"quote-{SYMBOL.lower()}")
    # A finished run keeps its branch's container name, so this is what makes the
    # example runnable twice.
    ws.clear_finished()

    try:
        out = ws.run(["python3", "-c", FETCH], allow=[QUOTE_HOST], timeout=90)
    except ApiError as e:
        print(f"the daemon refused the run: {e}", file=sys.stderr)
        return 1

    if out.exit_code != 0:
        # The commonest cause by far, and worth naming rather than printing a
        # traceback: the container could not reach the internet.
        print(f"the fetch failed (exit {out.exit_code}):\n{out.stderr}", file=sys.stderr)
        print(
            f"\nIf that is a network error, the daemon's egress posture does not permit "
            f"{QUOTE_HOST}. `allow` can add to what the daemon allows and never loosen it, "
            f"so a daemon started with `mode: none` refuses this by design.",
            file=sys.stderr,
        )
        return out.exit_code

    quote = json.loads(out.stdout)
    print(f"{quote['symbol']}  {quote['price']} {quote['currency']}  ({quote['exchange']})")

    # The agent variant, for when the question is not "what is the price" but
    # "what should I make of it" — same workspace, same isolation:
    #
    #   verdict = ws.agent("claude", f"Read this quote and say in one line whether "
    #                                f"it moved unusually today: {out.stdout}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
