// Command korb-fake runs an in-memory fake of the REWE API and login, so korb
// (and agents/skills using it) can be exercised end to end - including placing
// and modifying orders - without a real REWE account.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/miraassist/rewe-cli/internal/fakerewe"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address")
	verbose := flag.Bool("v", false, "log every request to stderr")
	flag.Parse()

	srv := fakerewe.New()
	if *verbose {
		srv.Log = func(s string) { log.Println(s) }
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	cfg := filepath.Join(os.TempDir(), "korb-fake-config")
	fmt.Printf(`korb-fake listening on %s

Use it with:

  export KORB_API_URL=%s/api
  export KORB_AUTH_URL=%s
  export KORB_CONFIG_DIR=%s

  korb login                  # logs in immediately (no browser needed)
  korb store set %s 12345    # delivery (or %s for pickup)

Fake world: minimum order value %d,%02d € (delivery), free delivery on Thursdays,
PayPal direct debit saved (no redirect), other online methods need a redirect,
orders can be modified until 12 hours before the slot. %d favorites are preset
(korb favorites): the favorite milk is not the first hit of "korb search milch",
tomatoes, pasta and rice are no favorites. State is kept in memory.
`, base, base, base, cfg, fakerewe.DeliveryMarketID, fakerewe.PickupMarketID,
		fakerewe.MinimumDelivery/100, fakerewe.MinimumDelivery%100, len(fakerewe.FavoriteProductIDs))
	log.Fatal(http.Serve(ln, srv))
}
