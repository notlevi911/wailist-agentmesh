package wallet_test

import (
	"context"
	"encoding/base64"
	"os"
	"testing"

	"github.com/algorand/go-algorand-sdk/v2/encoding/msgpack"
	"github.com/algorand/go-algorand-sdk/v2/types"

	"github.com/agentmesh/backend/internal/wallet"
)

func testWalletService(t *testing.T) *wallet.Service {
	t.Helper()
	url := os.Getenv("TEST_ALGOD_URL")
	if url == "" {
		t.Skip("TEST_ALGOD_URL not set")
	}
	return wallet.NewService("test-enc-key-32-bytes-long-12345", url, "", "testnet")
}

func TestSignUSDCPaymentGroupProducesTwoTxnsWithCorrectIndex(t *testing.T) {
	svc := testWalletService(t)
	_, encMnemonic, err := svc.GenerateWallet()
	if err != nil {
		t.Fatal(err)
	}

	group, idx, err := svc.SignUSDCPaymentGroup(context.Background(), encMnemonic,
		"LXPC4GQPYH2EZQX2QDYMHCP2I7MXIZMVRPIYTQ3D7R7HXJ4SIHCSYLF5YA",
		10458941, 100000,
		"ZMFK2OI7ZBD2U27ISERZC4S6LKM6WMFJPZQ4MYNJDZ2VNBNMBA67RA22AA")
	if err != nil {
		t.Fatal(err)
	}
	if len(group) != 2 {
		t.Fatalf("want 2-txn group, got %d", len(group))
	}
	if idx != 1 {
		t.Fatalf("want paymentIndex 1, got %d", idx)
	}

	// txn0 (fee-payer stub) must decode as unsigned (empty signature) —
	// GoPlausible's documented convention puts the fee payer first.
	raw0, err := base64.StdEncoding.DecodeString(group[0])
	if err != nil {
		t.Fatal(err)
	}
	var unsignedTxn types.Transaction
	if err := msgpack.Decode(raw0, &unsignedTxn); err != nil {
		t.Fatal(err)
	}
	if unsignedTxn.Type != types.PaymentTx {
		t.Fatalf("want fee-payer stub as PaymentTx, got %s", unsignedTxn.Type)
	}

	// txn1 must decode as a signed asset-transfer with the right amount.
	raw1, err := base64.StdEncoding.DecodeString(group[1])
	if err != nil {
		t.Fatal(err)
	}
	var stx types.SignedTxn
	if err := msgpack.Decode(raw1, &stx); err != nil {
		t.Fatal(err)
	}
	if stx.Txn.Type != types.AssetTransferTx {
		t.Fatalf("want AssetTransferTx, got %s", stx.Txn.Type)
	}
	if stx.Txn.AssetAmount != 100000 {
		t.Fatalf("want amount 100000, got %d", stx.Txn.AssetAmount)
	}
	if stx.Txn.XferAsset != 10458941 {
		t.Fatalf("want asset 10458941, got %d", stx.Txn.XferAsset)
	}
}

// TestSignUSDCPaymentGroupRepeatedIdenticalCallsProduceDistinctTxids guards
// against the exact bug that was silently failing ~1 in 4 platform-fee
// settlements: the fee is a flat amount paid from the same wallet to the
// same wallet every call, so two calls landing in the same algod round used
// to hash to byte-identical transactions -- algod then rejects the second
// as an exact duplicate even though nothing about the payment was invalid.
func TestSignUSDCPaymentGroupRepeatedIdenticalCallsProduceDistinctTxids(t *testing.T) {
	svc := testWalletService(t)
	_, encMnemonic, err := svc.GenerateWallet()
	if err != nil {
		t.Fatal(err)
	}

	const (
		payTo        = "LXPC4GQPYH2EZQX2QDYMHCP2I7MXIZMVRPIYTQ3D7R7HXJ4SIHCSYLF5YA"
		assetID      = 10458941
		amount       = 1_500_000 // the platform fee's exact flat amount
		feePayerAddr = "ZMFK2OI7ZBD2U27ISERZC4S6LKM6WMFJPZQ4MYNJDZ2VNBNMBA67RA22AA"
	)

	group1, _, err := svc.SignUSDCPaymentGroup(context.Background(), encMnemonic, payTo, assetID, amount, feePayerAddr)
	if err != nil {
		t.Fatal(err)
	}
	group2, _, err := svc.SignUSDCPaymentGroup(context.Background(), encMnemonic, payTo, assetID, amount, feePayerAddr)
	if err != nil {
		t.Fatal(err)
	}

	if group1[0] == group2[0] {
		t.Fatal("two identical-input calls produced byte-identical fee-payer stub txns -- would collide to the same txid if landed in the same algod round")
	}
	if group1[1] == group2[1] {
		t.Fatal("two identical-input calls produced byte-identical payment txns -- would collide to the same txid if landed in the same algod round")
	}
}
