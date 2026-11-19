// Command korb is a REWE pickup/delivery CLI with JSON output, built for agents.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	rewecli "github.com/miraassist/rewe-cli"
	"github.com/miraassist/rewe-cli/internal/auth"
	"github.com/miraassist/rewe-cli/internal/config"
	"github.com/miraassist/rewe-cli/internal/rewe"
)

var (
	allBoolFlags  = []string{"organic", "regional", "vegan", "vegetarian", "no-browser", "no-handler", "wait", "force"}
	allValueFlags = []string{"category", "page", "qty", "output", "timeout", "payment", "max-total"}
)

type app struct {
	ctx    context.Context
	args   args
	pretty bool
	doer   *rewe.Doer
	tokens *auth.Manager
	// baseCtx is the program's context; ctx may be narrowed per MCP call.
	baseCtx context.Context
	mcp     bool // serving MCP: stdout belongs to the protocol
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, usageText)
			os.Exit(2)
		}
		enc := json.NewEncoder(os.Stderr)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]string{"error": err.Error()})
		os.Exit(1)
	}
}

var errUsage = errors.New("usage")

func run(ctx context.Context, raw []string) error {
	a, err := parseArgs(raw, allBoolFlags, allValueFlags)
	if err != nil {
		return err
	}
	if a.bools["version"] {
		fmt.Println("korb " + rewecli.Version)
		return nil
	}
	if a.bools["help"] || len(a.pos) == 0 {
		fmt.Print(usageText)
		return nil
	}
	if a.pos[0] == "cert" {
		return cmdCert(ctx, a)
	}
	ap, err := newApp(ctx, a)
	if err != nil {
		return err
	}

	cmd := a.pos[0]
	ap.args.pos = a.pos[1:]
	handlers := map[string]func() error{
		"store":      ap.cmdStore,
		"search":     ap.cmdSearch,
		"favorites":  ap.cmdFavorites,
		"basket":     ap.cmdBasket,
		"timeslots":  ap.cmdTimeslots,
		"checkout":   ap.cmdCheckout,
		"orders":     ap.cmdOrders,
		"suggestion": ap.cmdSuggestion,
		"categories": ap.cmdCategories,
		"ebons":      ap.cmdEbons,
		"login":      ap.cmdLogin,
		"mcp":        ap.cmdMCP,
	}
	h, ok := handlers[cmd]
	if !ok {
		return fmt.Errorf("unknown command %q - see 'korb --help'", cmd)
	}
	return h()
}

func newApp(ctx context.Context, a args) (*app, error) {
	rewe.ReloadBaseURL()
	if (os.Getenv("KORB_API_URL") != "" || os.Getenv("KORB_AUTH_URL") != "") && os.Getenv("KORB_CONFIG_DIR") == "" {
		return nil, errors.New("KORB_API_URL/KORB_AUTH_URL are set: also set KORB_CONFIG_DIR so tokens of a test server never overwrite your real REWE session")
	}
	hc, err := httpClient(ctx)
	if err != nil {
		return nil, err
	}
	doer := &rewe.Doer{HTTP: hc, UserAgent: "korb/" + rewecli.Version}
	tokens, err := auth.NewManager(doer)
	if err != nil {
		return nil, err
	}
	return &app{ctx: ctx, baseCtx: ctx, args: a, pretty: a.bools["pretty"], doer: doer, tokens: tokens}, nil
}

// httpClient presents the REWE client certificate, fetched on first use. A
// plain-HTTP test server (korb-fake) does not need it.
func httpClient(ctx context.Context) (*http.Client, error) {
	if strings.HasPrefix(rewe.BaseURL, "http://") {
		return &http.Client{Timeout: 60 * time.Second}, nil
	}
	certPEM, keyPEM, err := rewecli.ClientCert(ctx)
	if err != nil {
		return nil, err
	}
	return rewe.NewHTTPClient(certPEM, keyPEM)
}

// cmdCert shows where the certificate is, or fetches it anew.
func cmdCert(ctx context.Context, a args) error {
	dir, err := rewecli.CertDir()
	if err != nil {
		return err
	}
	switch {
	case len(a.pos) == 2 && a.pos[1] == "fetch":
		if os.Getenv("KORB_CERT_DIR") != "" {
			return errors.New("KORB_CERT_DIR is set: korb does not write into a directory someone names")
		}
		if err := rewecli.FetchCert(ctx); err != nil {
			return err
		}
	case len(a.pos) != 1:
		return errors.New("usage: korb cert [fetch]")
	}
	_, err1 := os.Stat(filepath.Join(dir, "private.pem"))
	_, err2 := os.Stat(filepath.Join(dir, "private.key"))
	ap := &app{pretty: a.bools["pretty"]}
	return ap.print(map[string]any{"dir": dir, "present": err1 == nil && err2 == nil})
}

// only rejects flags that the current command does not understand.
func (ap *app) only(flags ...string) error {
	for name := range ap.args.bools {
		if name == "pretty" || slices.Contains(flags, name) {
			continue
		}
		return fmt.Errorf("flag --%s is not supported by this command", name)
	}
	for name := range ap.args.values {
		if !slices.Contains(flags, name) {
			return fmt.Errorf("flag --%s is not supported by this command", name)
		}
	}
	return nil
}

// sub pops the subcommand word, if any.
func (ap *app) sub() string {
	if len(ap.args.pos) == 0 {
		return ""
	}
	s := ap.args.pos[0]
	ap.args.pos = ap.args.pos[1:]
	return s
}

func (ap *app) print(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if ap.pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

// api returns a client bound to the selected store. Login is only enforced on
// endpoints that need it.
func (ap *app) api() (*rewe.API, config.Store, error) {
	s, err := config.ReadStore()
	if err != nil {
		return nil, s, err
	}
	m := rewe.Market{WwIdent: s.WwIdent, ZipCode: s.ZipCode, ServiceType: s.ServiceType}
	return rewe.NewAPI(ap.doer, m, ap.tokens.AccessToken), s, nil
}

// anonymousAPI is not bound to a market (store lookups).
func (ap *app) anonymousAPI() *rewe.API {
	return rewe.NewAPI(ap.doer, rewe.Market{}, nil)
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}

const usageText = `Usage: korb COMMAND [-p|--pretty] [-v|--version]
  REWE pickup / delivery CLI. All output is JSON (errors go to stderr as {"error": ...}).

Commands:

  korb store                       Show currently selected store
  korb store search <ZIP>          List pickup markets and the delivery service for a ZIP code
  korb store set <ID> <ZIP>        Select a market (PICKUP) or the delivery service (DELIVERY) by ID
  korb store info <ID>             Show REWE's details for a market ID

  korb search <QUERY|EAN>          Search products by name or EAN barcode (use * to browse all)
  korb search <Q> --organic        Filter by attribute (--organic, --regional, --vegan, --vegetarian)
  korb search <Q> --category <SLUG> Filter by category slug (repeatable, from korb categories)
  korb search <Q> --page <N>       Result page (40 products per page)

  korb favorites                   Show all favorite products across all lists
  korb favorites search <QUERY>    Filter favorites by name (case-insensitive, substring match)
  korb favorites add <LID> <PID>   Add product to default favorites list (listing ID + product ID)
  korb favorites delete <ITEM_ID>  Remove item from default favorites list (item ID from favorites)

  korb basket                      Show current basket with items, totals, and free pickup status
  korb basket add <LISTING_ID>     Set item quantity by listing ID. Overwrites existing quantity.
                                   --qty N sets absolute quantity. Default 1. Use --qty 0 to remove.

  korb timeslots                   List available timeslots

  korb checkout                    Show the checkout: total, timeslot, payment method, delivery address
  korb checkout payment-options    List payment methods; "usable" ones work without interaction
  korb checkout create <TIMESLOT_ID> Reserve the timeslot and set the payment method:
                                   PICKUP: payment in market. DELIVERY: a saved online method
                                   (e.g. PayPal direct debit). --payment KEY to choose explicitly.
  korb checkout order              Place the order. Timeslot and payment must be set first.
                                   --max-total CENTS (or $KORB_MAX_TOTAL in cents,
                                   $KORB_MAX_TOTAL_EURO in euros) refuses larger orders.

  korb orders                      Show open orders.
  korb orders history              Show all orders.
  korb orders get <ORDER_ID>       Get an order (with details) by id.
  korb orders delete <ORDER_ID>    Cancel an order by order ID.
  korb orders modify <ORDER_ID>    Start changing a placed order (while REWE allows it). Until
                                   submit/cancel, 'korb basket' and 'korb basket add' act on it.
  korb orders modify status        Show the running modification and its basket
  korb orders modify submit        Apply the changes (minimum order value and --max-total apply)
  korb orders modify cancel        Discard the changes; --force also clears korb's local state

  korb suggestion threshold <N>    Suggest N previously ordered items not yet in the basket,
                                   ranked by purchase frequency. Also reports the article value
                                   missing for free delivery/pickup and for the minimum order.

  korb categories                  List all product categories (with subcategories).

  korb ebons                       List digital receipts (eBons).
  korb ebons download <EBON_ID>    Download eBon PDF. --output FILE (default: ebon.pdf)

  korb cert                        Where the REWE app's client certificate is and whether it is there.
                                   korb fetches it on first use (from the korb repository it is a
                                   port of, checked against fixed SHA-256 sums) into its config
                                   directory; $KORB_CERT_DIR names a directory of your own instead.
  korb cert fetch                  Fetch it anew

  korb mcp                         Serve the shopping tools as an MCP server over stdio (for voice
                                   assistants). $KORB_MAX_TOTAL_EURO limits an order in euros.
                                   Its setup tools (login_start, login_complete, store_search,
                                   store_set) are for an admin page, not for a model.

  korb login                       Log in via the browser. On a local Linux desktop korb registers
                                   itself for de.rewe.app:// links and completes automatically.
                                   Over SSH / remote browsers: paste the redirect URL when asked.
                                   Without a terminal (agents) it prints {"status":"pending",
                                   "authUrl":...} and returns; finish with 'korb login complete'.
                                   --no-browser, --no-handler, --wait (non-interactive: wait for
                                   the local handler), --timeout 10m
  korb login complete <URL>        Finish a login with the de.rewe.app://redirect?... URL
  korb login status                Show whether a session is stored and when tokens expire
  korb login install-handler       (Re-)register the de.rewe.app:// handler (Linux)
  korb login uninstall-handler     Remove the handler again
`
