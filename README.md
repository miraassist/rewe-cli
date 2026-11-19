# rewe-cli

A command line client for REWE online shopping (pickup **and delivery**), built to be driven by agents and voice assistants. The binary is called `korb`, and all output is JSON.

> **Unofficial.** This project is not affiliated with or endorsed by REWE. It uses REWE's undocumented mobile API, which may change or block clients at any time. "REWE" is a trademark of the REWE Group. Use at your own risk.

rewe-cli is a Go port of [korb](https://github.com/yannick-cw/korb) by Yannick Gladow (Haskell), which did the reverse engineering together with [rewerse-engineering](https://github.com/ByteSizedMarius/rewerse-engineering). Compared to korb it adds:

- **No curl dependency**: plain Go `net/http` (HTTP/2, mTLS) and a single static binary.
- **Login that works for agents and over SSH** (see [Login](#login)).
- **Delivery (Lieferservice)**: search, basket, timeslots and ordering with a saved payment method (PayPal direct debit).
- **Order modification**: change a placed order until shortly before delivery.
- **Safety rails for agents**: an order amount limit, checks for the minimum order value with the missing amount, and a refusal to order with payment methods that need user input.
- **Safe token refresh**: REWE rotates refresh tokens, so concurrent calls are serialized with a file lock and tokens are stored with mode 0600.
- **`korb-fake`**: an in-memory fake REWE backend for developing agent skills without a real account.

## Install

Download `korb` (and `korb-fake`, optional) for Linux or macOS, x86_64 or arm64, from the [releases](https://github.com/miraassist/rewe-cli/releases); `SHA256SUMS` lists their checksums. Or build it with Go 1.24+:

```bash
go install github.com/miraassist/rewe-cli/cmd/korb@latest
go install github.com/miraassist/rewe-cli/cmd/korb-fake@latest   # optional
```

**The REWE app's client certificate** (REWE's API requires mTLS) is not part of this repository or its binaries. On first use korb fetches it from the [korb repository](https://github.com/yannick-cw/korb) it is a port of, at a fixed commit, checks it against fixed SHA-256 sums and keeps it in its config directory (`korb cert` shows where; `korb cert fetch` fetches it anew). `KORB_CERT_DIR` names a directory with `private.pem` and `private.key` of your own instead.

## Getting started

```bash
korb login                          # see below
korb store search 12345             # pickup markets + the delivery service for a ZIP code
korb store set <ID> 12345          # serviceType (PICKUP/DELIVERY) is detected automatically
korb search "hafermilch"
korb basket add <LISTING_ID> --qty 2
korb basket                         # shows violations, e.g. minimum order value not reached
korb timeslots
korb checkout create <TIMESLOT_ID>  # reserves the slot, selects the payment method
korb checkout                       # total, slot, payment method, delivery address
korb checkout order --max-total 15000
```

Run `korb --help` for all commands.

## Login

REWE's Keycloak only allows the browser-based authorization code flow for the app client. The device flow is blocked by REWE's firewall and password grants are disabled. The login redirects to the app URL `de.rewe.app://redirect?code=...`.

- **Local Linux desktop**: `korb login` registers itself as handler for `de.rewe.app://` links. After logging in, allow the browser to open the link with "korb REWE login", and the login completes automatically.
- **Browser on another machine (SSH)**: `korb login` prints the URL and asks for the redirect URL. Get it from the browser's developer tools. In Chrome the Console shows `Failed to launch 'de.rewe.app://redirect?...'`. In any browser, enable "Preserve log" in the Network tab and read the `Location` header of the last request.
- **Agents (no terminal)**: `korb login` returns `{"status":"pending","authUrl":...}` immediately. The agent sends the URL to the user, the user returns the redirect URL, and the agent runs `korb login complete '<url>'`. A login attempt stays valid for 30 minutes.

`korb login status` shows whether a session exists. Sessions use offline refresh tokens and normally do not expire.

## Delivery and payment

For delivery, REWE requires an online payment method. korb only orders with methods that need no user interaction: a saved method such as **PayPal direct debit** ("PayPal-Einzugsverfahren", set up once in the REWE app). `korb checkout payment-options` lists all methods and marks the usable ones.

- The **minimum order value** for delivery (currently 60 €) is based on the article value, excluding deposit. `korb checkout` refuses below it and names the missing amount. `korb suggestion threshold <N>` reports `remainingForMinimumOrderCents`.
- **Order limit**: `korb checkout order --max-total <cents>` or `$KORB_MAX_TOTAL` refuses larger orders. If both are set, the lower value applies.
- If REWE requests a payment confirmation in a browser (`redirectData`), korb stops before placing the order and returns the data.

## Changing a placed order

```bash
korb orders                      # open orders; "modify" in orderActions means changes are possible
korb orders modify <ORDER_ID>    # start; korb basket / korb basket add now act on this order
korb basket add <LISTING_ID> --qty 1
korb orders modify status
korb orders modify submit        # minimum order value and --max-total are checked
korb orders modify cancel        # discard the changes
```

Starting, changing and cancelling a modification follow recorded traffic of the REWE app. **Submitting** has not been observed yet: korb confirms and orders the modification checkout like a new order. Verify this with a real order before relying on it.

## MCP server for voice assistants

`korb mcp` serves the shopping tools over stdio as a [Model Context Protocol](https://modelcontextprotocol.io) server, for assistants that start it as a subprocess. Logging in and selecting the delivery service or market are for an admin: on the command line, or with the setup tools below from an admin page; an assistant should never offer them to its model.

| Tool | Purpose |
|---|---|
| `status` | login, service, basket, a payment method without interaction, delivery address; `missing` names what an admin has to set up |
| `basket_fill` | a whole shopping list into the basket in one call: per entry a matching favorite, else the product bought most often, else the first matching catalogue hit; unclear entries come back with options, nothing is guessed |
| `favorites_search`, `frequent`, `product_search` | find single products: favorites first, then the order history, then the catalogue |
| `basket`, `basket_set` | the basket with euro amounts as text, deposit, other fees and what is missing to the minimum order value |
| `slots`, `checkout_prepare` | slots, then reserve one (or the next one on a preferred weekday, or the next one) and select the saved payment method (orders nothing) |
| `order` | place the order; requires `total`, the amount told to the user, and refuses when the checkout's total differs |
| `orders`, `modification_start`, `modification_submit`, `modification_cancel`, `order_cancel` | open orders and changing or cancelling them |
| `login_start`, `login_complete`, `store_search`, `store_set` | the setup without a terminal, for an assistant's admin page (not for a model): a login in two steps (the redirect address is copied from the browser's console) and choosing the delivery service or a market |

The answers are compact JSON meant to be spoken (titles, prices like `"2,19 €"`, ids only where a later call needs them). `$KORB_MAX_TOTAL_EURO` (or `$KORB_MAX_TOTAL` in cents) is a hard limit per order. `order`, `modification_submit` and `order_cancel` are annotated as destructive: the assistant should ask the user before calling them.

```bash
KORB_CONFIG_DIR=/path/to/korb-config KORB_MAX_TOTAL_EURO=150 korb mcp
```

## korb-fake: practice without a REWE account

`korb-fake` imitates the REWE API and login in memory. It reproduces the behaviour observed on the real API: response envelopes, `412` on checkout below the minimum order value, free delivery on Thursdays, PayPal direct debit without redirect, other payment methods with redirect, rotating refresh tokens, and order modification until 12 hours before the slot.

It starts with a fixed order history and a fixed favorite list (`korb favorites`) for agent scenarios such as "milk is on the shopping list, use my favorite milk": `korb search milch` returns several milks, and neither the first hit nor the most often ordered milk is the favorite. Tomatoes, pasta and rice are not favorites, so the agent has to fall back to search or order history.

```bash
korb-fake &                      # prints the environment to use
export KORB_API_URL=http://127.0.0.1:8765/api KORB_AUTH_URL=http://127.0.0.1:8765 KORB_CONFIG_DIR=/tmp/korb-fake-config
korb login                       # logs in immediately against a plain-HTTP test server
korb store set 2000001 12345     # delivery; 1000001 for pickup
```

korb refuses to run against a different API unless `KORB_CONFIG_DIR` is also set, so a test server can never overwrite your real session.

## Configuration

| Variable | Purpose |
|---|---|
| `KORB_CONFIG_DIR` | config directory (default `$XDG_CONFIG_HOME/korb` or `~/.config/korb`, shared with korb) |
| `KORB_CERT_DIR` | a directory of your own with `private.pem`/`private.key` (korb then fetches nothing) |
| `KORB_MAX_TOTAL` | default order limit in cents |
| `KORB_MAX_TOTAL_EURO` | the same in euros (e.g. `150`); the lower limit wins |
| `KORB_API_URL`, `KORB_AUTH_URL` | alternative backend (e.g. korb-fake) |

## Development

```bash
go test ./...        # unit tests + end-to-end tests against the fake
go vet ./... && staticcheck ./...
```

`api/` contains the reverse-engineered OpenAPI descriptions from korb.
