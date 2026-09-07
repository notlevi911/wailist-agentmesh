// Command x402pay is a one-shot x402 *client*: it pays a real, payment-gated
// endpoint once and prints what happened. Everything else in this repo plays
// the resource-server role (issue a 402, verify, settle); this plays the
// payer, which is otherwise only reachable by driving a whole workflow run.
//
// It exists mainly for one job that has no other clean trigger: the
// facilitator only catalogs a route in the Bazaar once it observes a valid
// discovery declaration riding on a *settled* payment, never on the
// informational challenge alone. So after a fix to that declaration ships,
// something has to make one genuine paid call before the catalog state
// changes. Hand-rolling that means reproducing the challenge decode, the
// fee-pooled group signing, and the header encoding -- all of which already
// exist here, in the exact form the relay itself uses.
//
// THIS SPENDS REAL MONEY. On mainnet the asset is real USDC and the transfer
// is irreversible. Guards, in order:
//
//   - It refuses to pay without -confirm. Without it, the command probes the
//     endpoint, prints the challenge and the exact amount it would send, and
//     exits 0 without signing anything. Run it that way first, every time.
//   - It refuses any quote above -max-micros (default $0.05).
//   - It refuses any asset other than -asset-id, so a target quoting some
//     other ASA can never be paid by accident.
//
// Secrets are read from the environment, not flags: a flag value is visible
// in `ps` output and lands in shell history. Set ENCRYPTION_KEY and
// X402PAY_ENC_MNEMONIC (or PLATFORM_SPEND_WALLET_ENC_MNEMONIC, which the
// deployed backend already defines) before running.
//
// Typical use -- dry run, then the real thing:
//
//	go run ./cmd/x402pay
//	go run ./cmd/x402pay -confirm
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/wallet"
)

// Mainnet/testnet constants, kept identical to cmd/server/main.go's own
// defaults. relayNetwork is the CAIP-style genesis-hash identifier the x402
// payload carries, which is a different string from the human network name
// the wallet service takes -- both are derived from -network here so they
// cannot disagree.
const (
	mainnetUSDCAssetID = 31566704
	testnetUSDCAssetID = 10458941
	mainnetRelayNet    = "algorand:wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8="
	testnetRelayNet    = "algorand:SGO1GKSzyE7IEPItTxCByw9x8FmnrCDexi9/cOUJOiI="
)

func main() {
	log.SetFlags(0)

	endpoint := flag.String("endpoint", "https://www.agent-mesh.app/api/x402/relay",
		"x402 endpoint to pay. Defaults to the relay's own self-listing -- the bare route with no ?target=, which is the one that declares the relay itself as the catalog resource")
	method := flag.String("method", "GET", "HTTP method for the paid request")
	network := flag.String("network", "mainnet", "algorand network: mainnet or testnet")
	assetID := flag.Uint64("asset-id", 0, "USDC ASA id to accept in the quote; defaults to the -network default")
	maxMicros := flag.Int64("max-micros", 50000, "refuse to pay a quote above this many micro-USDC (50000 = $0.05)")
	algodURL := flag.String("algod-url", "", "algod REST endpoint; defaults to the -network public node")
	algodToken := flag.String("algod-token", "", "algod API token, if the endpoint requires one")
	timeout := flag.Duration("timeout", 90*time.Second, "overall deadline for the probe and the paid call")
	confirm := flag.Bool("confirm", false, "actually sign and send the payment. Without this the command only probes and reports what it would pay")
	flag.Parse()

	if *network != "mainnet" && *network != "testnet" {
		log.Fatalf("-network must be mainnet or testnet, got %q", *network)
	}
	if *maxMicros <= 0 {
		// PayTargetFromWallet2 treats MaxRelayOutboundUSDMicros <= 0 as "no
		// cap" (that's the correct default for server-side relay config,
		// where 0 means unconfigured). Here it would silently invert the
		// documented guarantee that this command refuses any quote above
		// -max-micros, so reject it outright instead of passing it through.
		log.Fatalf("-max-micros must be a positive number of micro-USDC, got %d (0 or negative disables the spend cap entirely)", *maxMicros)
	}
	relayNetwork := testnetRelayNet
	defaultAsset := uint64(testnetUSDCAssetID)
	defaultAlgod := "https://testnet-api.algonode.cloud"
	if *network == "mainnet" {
		relayNetwork = mainnetRelayNet
		defaultAsset = mainnetUSDCAssetID
		defaultAlgod = "https://mainnet-api.algonode.cloud"
	}
	if *assetID == 0 {
		*assetID = defaultAsset
	}
	if *algodURL == "" {
		*algodURL = defaultAlgod
	}

	encKey := os.Getenv("ENCRYPTION_KEY")
	// Credentials are needed only to sign. A dry run reads a public, unpaid
	// 402 and reports it, so it deliberately works with no secrets in the
	// environment at all -- that makes it usable as a plain challenge
	// inspector, and means the common case of checking an endpoint never
	// puts a mnemonic anywhere near the process.
	//
	// PLATFORM_SPEND_WALLET_ENC_MNEMONIC is Wallet 1, the account that
	// already funds every relayed call, so paying our own relay from it is
	// the same Wallet 1 -> Wallet 2 direction the engine's own self-settle
	// paths use. X402PAY_ENC_MNEMONIC overrides it for paying from some
	// other account.
	encMnemonic := os.Getenv("X402PAY_ENC_MNEMONIC")
	if encMnemonic == "" {
		encMnemonic = os.Getenv("PLATFORM_SPEND_WALLET_ENC_MNEMONIC")
	}
	if *confirm {
		if encKey == "" {
			log.Fatal("ENCRYPTION_KEY is not set -- it must match the ENCRYPTION_KEY the encrypted mnemonic was produced under, or the mnemonic will not decrypt")
		}
		if encMnemonic == "" {
			log.Fatal("set X402PAY_ENC_MNEMONIC (or PLATFORM_SPEND_WALLET_ENC_MNEMONIC) to the payer's ENCRYPTION_KEY-encrypted mnemonic")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	walletSvc := wallet.NewService(encKey, *algodURL, *algodToken, *network)
	payerAddr := "(not resolved -- dry run needs no credentials)"
	if encKey != "" && encMnemonic != "" {
		addr, err := walletSvc.AddressForEncMnemonic(encMnemonic)
		if err != nil {
			log.Fatalf("could not derive the payer address from the encrypted mnemonic: %v", err)
		}
		payerAddr = addr
	}

	fmt.Printf("payer     : %s\n", payerAddr)
	fmt.Printf("endpoint  : %s %s\n", *method, *endpoint)
	fmt.Printf("network   : %s (asset %d)\n\n", *network, *assetID)

	// Probe first, always -- this is an unpaid request, so it is free and
	// safe to run regardless of -confirm.
	isV2, quote, err := nodes.ProbeX402Quote(ctx, *endpoint, *method)
	if err != nil {
		log.Fatalf("could not read a 402 challenge from the endpoint: %v", err)
	}
	if !isV2 {
		log.Fatal("endpoint did not answer with an x402 v2 challenge -- refusing to pay a dialect this command cannot verify")
	}

	fmt.Println("challenge:")
	fmt.Printf("  payTo      : %s\n", quote.PayTo)
	fmt.Printf("  asset      : %s\n", quote.Asset)
	fmt.Printf("  amount     : %s micro-USDC\n", quote.MaxAmountRequired)
	fmt.Printf("  feePayer   : %s\n", orNone(quote.FeePayer))
	fmt.Printf("  bazaar     : %s\n", bazaarSummary(quote.RawChallenge))

	if !*confirm {
		fmt.Printf("\nDRY RUN -- nothing signed, nothing spent.\n")
		fmt.Printf("Re-run with -confirm to send %s micro-USDC to %s.\n", quote.MaxAmountRequired, quote.PayTo)
		return
	}

	cfg := nodes.Wallet2PayConfig{
		USDCSigner:                walletSvc,
		PlatformWalletEncMnemonic: encMnemonic,
		USDCAssetID:               *assetID,
		RelayNetwork:              relayNetwork,
		// The hard spend ceiling. PayTargetFromWallet2 refuses any quote
		// above this before it signs anything.
		MaxRelayOutboundUSDMicros: *maxMicros,
	}

	fmt.Printf("\npaying...\n")
	result, err := nodes.PayTargetFromWallet2(ctx, cfg, *endpoint, *method, nil, quote)
	if err != nil {
		// result.Signed distinguishes the two failure modes that matter:
		// money definitely never moved, versus a signed payment whose
		// outcome this command could not read back. They call for opposite
		// responses -- retry, versus check the chain before doing anything.
		var payErr *nodes.Wallet2PayError
		if errors.As(err, &payErr) {
			err = fmt.Errorf("%s (status %d)", payErr.Msg, payErr.StatusCode)
		}
		if result.Signed {
			log.Fatalf("PAYMENT WAS SIGNED but the call did not complete cleanly: %v\n"+
				"Do NOT simply re-run this -- check %s on an explorer first, the transfer may already have landed.", err, payerAddr)
		}
		log.Fatalf("payment failed before anything was signed (no money moved): %v", err)
	}

	fmt.Printf("signed    : %t\n", result.Signed)
	fmt.Printf("settled   : %t\n", result.Settled)
	fmt.Printf("status    : %d\n", result.StatusCode)
	if result.OutboundTxID != "" {
		fmt.Printf("txid      : %s\n", result.OutboundTxID)
	}
	if len(result.ResponseBody) > 0 {
		fmt.Printf("response  : %s\n", truncate(string(result.ResponseBody), 600))
	}
	if !result.Settled {
		os.Exit(1)
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none -- self-funded single transaction)"
	}
	return s
}

// bazaarSummary reports whether the challenge carries a discovery declaration
// and whether its two method halves agree, since a mismatch there is exactly
// what makes a settlement fail to catalog while still reporting success --
// worth seeing before spending, not after.
func bazaarSummary(challenge map[string]any) string {
	exts, _ := challenge["extensions"].(map[string]any)
	bazaar, _ := exts["bazaar"].(map[string]any)
	if bazaar == nil {
		return "absent -- this settlement will not catalog"
	}
	info, _ := bazaar["info"].(map[string]any)
	input, _ := info["input"].(map[string]any)
	declared, _ := input["method"].(string)

	schema, _ := bazaar["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	inSchema, _ := props["input"].(map[string]any)
	inProps, _ := inSchema["properties"].(map[string]any)
	methodSchema, _ := inProps["method"].(map[string]any)

	raw, _ := json.Marshal(methodSchema["enum"])
	var enum []any
	_ = json.Unmarshal(raw, &enum)

	if len(enum) == 1 && enum[0] == declared {
		return fmt.Sprintf("present, method %q matches enum %s", declared, raw)
	}
	return fmt.Sprintf("PRESENT BUT INVALID -- declared %q against enum %s; the catalog validator will drop this", declared, raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "... (truncated)"
}
