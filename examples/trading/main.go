// Command trading demonstrates the FORTS order-entry flow over FIX Gate:
// Connect (Logon), CreateOrder, watch its Execution Report via
// WatchOpenOrders, then CancelOrder.
//
// Requires exchange/broker-issued FIX Gate credentials — will not run
// against a public endpoint. Set the environment variables below before
// running (see docs/handoff.md "Open questions" for how to obtain them):
//
//	MOEX_FIX_HOST            (default: moex.DefaultFIXHostDSP)
//	MOEX_FIX_SENDER_COMP_ID  (required)
//	MOEX_FIX_ACCOUNT         (required — FORTS trading account code)
//	MOEX_FORTS_SYMBOL        (default: "Si-12.26")
//
// Run:
//
//	MOEX_FIX_SENDER_COMP_ID=... MOEX_FIX_ACCOUNT=... go run ./examples/trading
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/shopspring/decimal"

	moex "github.com/tonymontanov/go-moex"
	"github.com/tonymontanov/go-moex/forts"
	"github.com/tonymontanov/go-moex/forts/types"
)

func main() {
	var senderCompID string = os.Getenv("MOEX_FIX_SENDER_COMP_ID")
	var account string = os.Getenv("MOEX_FIX_ACCOUNT")
	if senderCompID == "" || account == "" {
		log.Fatal("MOEX_FIX_SENDER_COMP_ID and MOEX_FIX_ACCOUNT are required (see file header for how to obtain them)")
	}
	var symbol string = envOr("MOEX_FORTS_SYMBOL", "Si-12.26")

	var cfg moex.Config = moex.DefaultConfig()
	cfg.FIX.SenderCompID = senderCompID
	if host := os.Getenv("MOEX_FIX_HOST"); host != "" {
		cfg.FIX.Host = host
	}

	var client, err = moex.NewClient(cfg)
	if err != nil {
		log.Fatalf("moex.NewClient: %v", err)
	}
	defer client.Close()

	var fortsClient *forts.Client = client.Forts().(*forts.Client)

	var ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err = fortsClient.Connect(ctx); err != nil {
		log.Fatalf("Connect (FIX Logon): %v", err)
	}
	log.Println("FIX Logon succeeded")

	var watchCtx, watchCancel = context.WithCancel(context.Background())
	defer watchCancel()
	var updates = fortsClient.Trading().WatchOpenOrders(watchCtx)
	go func() {
		for order := range updates {
			log.Printf("order update: id=%d status=%s leaves=%d cum=%d", order.OrderID, order.Status, order.LeavesQty, order.CumQty)
		}
	}()

	// FORTS FIX Gate has no Market OrdType — send a deliberately
	// non-aggressive limit far from the market so this demo order rests
	// instead of filling immediately (see CreateOrder doc).
	var info, cErr = fortsClient.Trading().CreateOrder(ctx, types.CreateOrderRequest{
		Symbol:      symbol,
		Side:        types.SideBuy,
		Quantity:    1,
		Price:       decimal.NewFromInt(1), // intentionally far off-market; cancel below.
		Account:     account,
		TimeInForce: types.TimeInForceDay,
	})
	if cErr != nil {
		log.Fatalf("CreateOrder: %v", cErr)
	}
	log.Printf("order accepted: id=%d status=%s", info.OrderID, info.Status)

	time.Sleep(2 * time.Second)

	var cancelInfo, cancelErr = fortsClient.Trading().CancelOrder(ctx, types.CancelOrderRequest{
		OrderID:  info.OrderID,
		Symbol:   symbol,
		Side:     types.SideBuy,
		Quantity: 1,
	})
	if cancelErr != nil {
		log.Fatalf("CancelOrder: %v", cancelErr)
	}
	log.Printf("order canceled: id=%d status=%s", cancelInfo.OrderID, cancelInfo.Status)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
