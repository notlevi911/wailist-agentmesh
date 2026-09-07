package wallet_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/agentmesh/backend/internal/wallet"
)

const testEncKey = "0123456789abcdef0123456789abcdef"

func TestGenerateWallet(t *testing.T) {
	svc := wallet.NewService("0123456789abcdef0123456789abcdef",
		"https://testnet-api.algonode.cloud", "", "testnet")

	address, encMnemonic, err := svc.GenerateWallet()
	if err != nil {
		t.Fatal(err)
	}
	if len(address) < 50 {
		t.Fatalf("address too short: %q", address)
	}
	if encMnemonic == "" {
		t.Fatal("no encrypted mnemonic")
	}

	mnem, err := svc.DecryptMnemonic(encMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	words := strings.Fields(mnem)
	if len(words) != 25 {
		t.Fatalf("want 25 words got %d", len(words))
	}
}

// Tendril's wallet-login proves address control with a 0-amount self-payment
// that is verified and discarded, never broadcast. It must therefore be
// signable with no algod round trip and no balance whatsoever.
func TestSignZeroSelfPaymentIsOfflineAndSelfAddressed(t *testing.T) {
	svc := wallet.NewService(testEncKey, "http://unreachable.invalid", "", "mainnet")
	addr, encMnemonic, err := svc.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet: %v", err)
	}

	signed, gotAddr, err := svc.SignZeroSelfPayment(
		context.Background(), encMnemonic, "NONCE-1",
		"wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8=", "mainnet-v1.0",
	)
	if err != nil {
		t.Fatalf("SignZeroSelfPayment: %v", err)
	}
	if gotAddr != addr {
		t.Errorf("address = %q, want %q", gotAddr, addr)
	}
	if signed == "" {
		t.Error("signed txn is empty")
	}
	if _, err := base64.StdEncoding.DecodeString(signed); err != nil {
		t.Errorf("signed txn is not base64: %v", err)
	}
}

// An empty note is the "just tell me the address" probe the session's login
// makes before it knows which nonce to sign.
func TestSignZeroSelfPaymentEmptyNoteStillDerivesAddress(t *testing.T) {
	svc := wallet.NewService(testEncKey, "http://unreachable.invalid", "", "mainnet")
	addr, encMnemonic, err := svc.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet: %v", err)
	}
	_, gotAddr, err := svc.SignZeroSelfPayment(
		context.Background(), encMnemonic, "",
		"wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8=", "mainnet-v1.0",
	)
	if err != nil {
		t.Fatalf("SignZeroSelfPayment: %v", err)
	}
	if gotAddr != addr {
		t.Errorf("address = %q, want %q", gotAddr, addr)
	}
}
