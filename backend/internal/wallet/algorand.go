package wallet

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/algorand/go-algorand-sdk/v2/client/v2/algod"
	"github.com/algorand/go-algorand-sdk/v2/crypto"
	"github.com/algorand/go-algorand-sdk/v2/encoding/msgpack"
	"github.com/algorand/go-algorand-sdk/v2/mnemonic"
	"github.com/algorand/go-algorand-sdk/v2/transaction"
	"github.com/algorand/go-algorand-sdk/v2/types"
)

// uniqueNote appends 8 random bytes to tag so repeated x402 payments with
// identical sender/receiver/amount/asset never produce byte-identical --
// and therefore same-txid -- transactions when two such calls land within
// the same algod round (SuggestedParams' FirstValid/LastValid only change
// per round, not per call). This matters most for the platform's own flat
// per-call markup (SettlePlatformFee/runfund.go), which pays the exact
// same amount from the exact same wallet to the exact same wallet on
// every single call: without a distinguishing note, two such settlements
// landing in the same ~3s round hash identically, and algod rejects the
// second as an exact duplicate ("transaction already in ledger") even
// though nothing about the payment itself was invalid. Confirmed as the
// dominant cause behind that leg's elevated verify-without-settle rate
// (facilitator.goplausible.xyz: ~27.5% for the flat-amount platform fee
// vs ~9.5% for the main relay leg, whose amount varies per vendor quote
// and so rarely collides).
//
// Propagates rand.Read's error rather than falling back to a zeroed nonce
// -- matching this codebase's existing randHex/randURLSafe/randomHex32
// convention (oauth.go, connector_oauth.go, connectors_devtools.go). A
// silently-swallowed CSPRNG failure would make every note the same
// constant string during the outage, reintroducing the exact collision
// this function exists to eliminate with no log line or metric to say so;
// callers below fail the payment attempt instead, which is loud and
// matches how a signing failure is already handled everywhere else in
// this file.
func uniqueNote(tag string) ([]byte, error) {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate unique payment note: %w", err)
	}
	return []byte(tag + ":" + hex.EncodeToString(nonce)), nil
}

type Service struct {
	encKey     string
	algodURL   string
	algodToken string
	network    string
}

func NewService(encKey, algodURL, algodToken, network string) *Service {
	return &Service{encKey: encKey, algodURL: algodURL, algodToken: algodToken, network: network}
}

func (s *Service) Network() string { return s.network }

func (s *Service) GenerateWallet() (address, encMnemonic string, err error) {
	acc := crypto.GenerateAccount()
	mn, err := mnemonic.FromPrivateKey(acc.PrivateKey)
	if err != nil {
		return "", "", err
	}
	enc, err := Encrypt(mn, s.encKey)
	if err != nil {
		return "", "", err
	}
	return acc.Address.String(), enc, nil
}

func (s *Service) DecryptMnemonic(encMnemonic string) (string, error) {
	return Decrypt(encMnemonic, s.encKey)
}

// AddressForEncMnemonic decrypts encMnemonic and derives the Algorand
// address it controls. Used at startup to verify an operator-supplied
// address env var actually matches the operator-supplied encrypted
// mnemonic env var it's paired with -- pasting the right mnemonic under the
// wrong address label (or vice versa) would otherwise go undetected until
// a payment silently signs from a different account than the one the rest
// of the system believes it's using.
func (s *Service) AddressForEncMnemonic(encMnemonic string) (string, error) {
	mn, err := s.DecryptMnemonic(encMnemonic)
	if err != nil {
		return "", err
	}
	privKey, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return "", err
	}
	acc, err := crypto.AccountFromPrivateKey(privKey)
	if err != nil {
		return "", err
	}
	return acc.Address.String(), nil
}

func (s *Service) Balance(ctx context.Context, address string) (uint64, error) {
	client, err := algod.MakeClient(s.algodURL, s.algodToken)
	if err != nil {
		return 0, err
	}
	info, err := client.AccountInformation(address).Do(ctx)
	if err != nil {
		return 0, err
	}
	return info.Amount, nil
}

func (s *Service) FundFromDispenser(ctx context.Context, address string, amount uint64) (string, error) {
	url := fmt.Sprintf("https://dispenser.testnet.aws.algodev.network/?receiver=%s&amount=%d", address, amount)
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		TxID string `json:"txId"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.TxID, nil
}

func (s *Service) SignAndSendPayment(ctx context.Context, encMnemonic, toAddress string, microAlgo uint64) (string, error) {
	mn, err := s.DecryptMnemonic(encMnemonic)
	if err != nil {
		return "", err
	}
	privKey, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return "", err
	}
	acc, err := crypto.AccountFromPrivateKey(privKey)
	if err != nil {
		return "", err
	}

	client, err := algod.MakeClient(s.algodURL, s.algodToken)
	if err != nil {
		return "", err
	}
	params, err := client.SuggestedParams().Do(ctx)
	if err != nil {
		return "", err
	}
	// uniqueNote, not nil: this is the same "identical sender/receiver/amount
	// within one algod round hashes identically" collision uniqueNote's own
	// doc comment describes, on the legacy ALGO-direct-pay dialect
	// (ExecuteTool402's older non-USDC-relay path) rather than the USDC
	// group-signing path that comment was written for -- an agent calling
	// the same tool402 endpoint twice in quick succession with an unchanged
	// quoted price would otherwise produce byte-identical transactions and
	// have the second rejected by algod as an exact duplicate.
	note, err := uniqueNote("x402-legacy-pay")
	if err != nil {
		return "", err
	}
	txn, err := transaction.MakePaymentTxn(acc.Address.String(), toAddress, microAlgo, note, "", params)
	if err != nil {
		return "", err
	}
	_, signed, err := crypto.SignTransaction(privKey, txn)
	if err != nil {
		return "", err
	}
	txID, err := client.SendRawTransaction(signed).Do(ctx)
	if err != nil {
		return "", err
	}
	return txID, nil
}

func (s *Service) OptInAsset(ctx context.Context, encMnemonic string, assetID uint64) (string, error) {
	mn, err := s.DecryptMnemonic(encMnemonic)
	if err != nil {
		return "", err
	}
	privKey, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return "", err
	}
	acc, err := crypto.AccountFromPrivateKey(privKey)
	if err != nil {
		return "", err
	}

	client, err := algod.MakeClient(s.algodURL, s.algodToken)
	if err != nil {
		return "", err
	}
	params, err := client.SuggestedParams().Do(ctx)
	if err != nil {
		return "", err
	}
	txn, err := transaction.MakeAssetAcceptanceTxn(acc.Address.String(), nil, params, assetID)
	if err != nil {
		return "", err
	}
	_, signed, err := crypto.SignTransaction(privKey, txn)
	if err != nil {
		return "", err
	}
	return client.SendRawTransaction(signed).Do(ctx)
}

// SignUSDCPaymentGroup builds a 2-txn atomic group for a gasless USDC payment:
// txn0 is the caller's signed asset-transfer (Fee=0, fee-pooled), txn1 is an
// unsigned payment-stub from feePayerAddr to itself that carries both txns'
// fees — the facilitator cosigns txn1 during /settle, so the caller's wallet
// never needs a standing ALGO balance for fees. Returns both txns base64
// (msgpack)-encoded in group order, and which index holds the real payment.
func (s *Service) SignUSDCPaymentGroup(ctx context.Context, encMnemonic, payTo string, assetID, amountMicros uint64, feePayerAddr string) (paymentGroup []string, paymentIndex int, err error) {
	mn, err := s.DecryptMnemonic(encMnemonic)
	if err != nil {
		return nil, 0, err
	}
	privKey, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return nil, 0, err
	}
	acc, err := crypto.AccountFromPrivateKey(privKey)
	if err != nil {
		return nil, 0, err
	}

	client, err := algod.MakeClient(s.algodURL, s.algodToken)
	if err != nil {
		return nil, 0, err
	}
	params, err := client.SuggestedParams().Do(ctx)
	if err != nil {
		return nil, 0, err
	}

	payNote, err := uniqueNote("x402-payment-v2")
	if err != nil {
		return nil, 0, err
	}
	payTxn, err := transaction.MakeAssetTransferTxn(acc.Address.String(), payTo, amountMicros, payNote, params, "", assetID)
	if err != nil {
		return nil, 0, err
	}
	payTxn.Fee = 0 // fee-pooled: the stub below covers both txns' fees

	feeStubNote, err := uniqueNote("x402-fee-payer")
	if err != nil {
		return nil, 0, err
	}
	feeStub, err := transaction.MakePaymentTxn(feePayerAddr, feePayerAddr, 0, feeStubNote, "", params)
	if err != nil {
		return nil, 0, err
	}
	feeStub.Fee = types.MicroAlgos(params.MinFee * 2) // covers this txn + the fee-pooled payment txn

	// Group order matches GoPlausible's documented convention: [0] is the
	// unsigned fee-payer stub, [1] is the signed payment — not the reverse.
	// The facilitator reads group[0] as the fee payer when computing the
	// pooled fee; sending the signed (Fee-omitted, since it's 0) payment
	// there instead made it read an absent field as undefined and crash
	// (confirmed live: "Cannot convert undefined to a BigInt").
	grouped, err := transaction.AssignGroupID([]types.Transaction{feeStub, payTxn}, "")
	if err != nil {
		return nil, 0, err
	}

	unsignedStubBytes := msgpack.Encode(grouped[0])
	_, signedPay, err := crypto.SignTransaction(privKey, grouped[1])
	if err != nil {
		return nil, 0, err
	}

	return []string{
		base64.StdEncoding.EncodeToString(unsignedStubBytes),
		base64.StdEncoding.EncodeToString(signedPay),
	}, 1, nil
}

// SignUSDCPaymentSingle builds and signs one standard, self-fee-paying USDC
// asset-transfer transaction — the plain x402 "exact" scheme on Algorand,
// for a target whose own challenge names no accepts[0].extra.feePayer.
// A target that DOES name one is asking for SignUSDCPaymentGroup's
// fee-pooled convention instead (confirmed live 2026-08-01: a real mainnet
// target, arbsignal-production.up.railway.app, names the same shared
// ecosystem fee payer our own inbound leg already uses, and its middleware
// verifies/settles that group through a facilitator exactly like our own
// /x402/relay does — nothing about a third party needs to "cosign" the
// stub itself). PayTargetFromWallet2 (walletpay.go) is the one call site
// that picks between this and SignUSDCPaymentGroup, based on that field.
func (s *Service) SignUSDCPaymentSingle(ctx context.Context, encMnemonic, payTo string, assetID, amountMicros uint64) (paymentGroup []string, paymentIndex int, err error) {
	mn, err := s.DecryptMnemonic(encMnemonic)
	if err != nil {
		return nil, 0, err
	}
	privKey, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return nil, 0, err
	}
	acc, err := crypto.AccountFromPrivateKey(privKey)
	if err != nil {
		return nil, 0, err
	}

	client, err := algod.MakeClient(s.algodURL, s.algodToken)
	if err != nil {
		return nil, 0, err
	}
	params, err := client.SuggestedParams().Do(ctx)
	if err != nil {
		return nil, 0, err
	}

	payNote, err := uniqueNote("x402-payment-v2")
	if err != nil {
		return nil, 0, err
	}
	payTxn, err := transaction.MakeAssetTransferTxn(acc.Address.String(), payTo, amountMicros, payNote, params, "", assetID)
	if err != nil {
		return nil, 0, err
	}
	// Fee left at params' suggested value (unlike SignUSDCPaymentGroup's
	// zeroed, pool-covered fee) — the sender covers its own fee directly,
	// which is what any standard-conformant facilitator/middleware expects
	// from a single-transaction "exact" scheme payment.

	_, signed, err := crypto.SignTransaction(privKey, payTxn)
	if err != nil {
		return nil, 0, err
	}

	return []string{base64.StdEncoding.EncodeToString(signed)}, 0, nil
}

// SignZeroSelfPayment signs a 0-amount payment from an address to itself,
// carrying note in the note field, using hardcoded suggested params rather
// than any algod round trip.
//
// Tendril's /auth/wallet-login verifies this signature and then discards the
// transaction — it is never broadcast. That is why the params below are
// invented rather than fetched: a transaction nobody submits has no real
// validity window to respect, and requiring algod here would make logging in
// to read a balance fail whenever the node is slow. It also costs nothing and
// requires no balance, which matters because Wallet 2's ALGO is not this
// feature's concern.
//
// Returns the base64 signed transaction and the signing address.
func (s *Service) SignZeroSelfPayment(ctx context.Context, encMnemonic, note, genesisHashB64, genesisID string) (string, string, error) {
	mn, err := s.DecryptMnemonic(encMnemonic)
	if err != nil {
		return "", "", err
	}
	privateKey, err := mnemonic.ToPrivateKey(mn)
	if err != nil {
		return "", "", err
	}
	acct, err := crypto.AccountFromPrivateKey(privateKey)
	if err != nil {
		return "", "", err
	}
	addr := acct.Address.String()

	genesisHash, err := base64.StdEncoding.DecodeString(genesisHashB64)
	if err != nil {
		return "", "", fmt.Errorf("genesis hash: %w", err)
	}
	params := types.SuggestedParams{
		Fee:             1000,
		MinFee:          1000,
		FirstRoundValid: 1,
		LastRoundValid:  1000,
		GenesisID:       genesisID,
		GenesisHash:     genesisHash,
		FlatFee:         true,
	}
	txn, err := transaction.MakePaymentTxn(addr, addr, 0, []byte(note), "", params)
	if err != nil {
		return "", "", err
	}
	_, signed, err := crypto.SignTransaction(privateKey, txn)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(signed), addr, nil
}
