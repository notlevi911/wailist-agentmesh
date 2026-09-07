package tendril

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// NonceSigner produces the 0-amount self-payment Tendril's wallet-login
// expects. The transaction is verified and discarded, never broadcast, so it
// costs nothing and needs no balance — it exists purely to prove control of
// the address whose Tendril credit balance we are about to read.
//
// wallet.Service implements this (Task 3).
type NonceSigner interface {
	SignZeroSelfPayment(ctx context.Context, encMnemonic, note, genesisHashB64, genesisID string) (signedTxnB64 string, address string, err error)
}

type Session struct {
	client      *Client
	signer      NonceSigner
	encMnemonic string
	network     string

	mu    sync.Mutex
	token string
}

// genesisParts splits a CAIP-2 id like
// "algorand:wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8=" into the base64
// genesis hash and the matching genesis id string algod expects.
func genesisParts(caip2 string) (hashB64, genesisID string) {
	_, hashB64, _ = strings.Cut(caip2, ":")
	switch hashB64 {
	case "wGHE2Pwdvd7S12BL5FaOP20EGYesN73ktiC1qzkkit8=":
		return hashB64, "mainnet-v1.0"
	default:
		return hashB64, "testnet-v1.0"
	}
}

func (c *Client) Session(ctx context.Context, signer NonceSigner, encMnemonic string) (*Session, error) {
	p, err := c.Platform(ctx)
	if err != nil {
		return nil, fmt.Errorf("tendril session: platform: %w", err)
	}
	s := &Session{client: c, signer: signer, encMnemonic: encMnemonic, network: p.Network}
	if _, err := s.login(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) login(ctx context.Context) (string, error) {
	hashB64, genesisID := genesisParts(s.network)

	// The address is whatever the signer derives from the mnemonic; ask it
	// first with an empty note so the nonce is requested for the right address.
	_, address, err := s.signer.SignZeroSelfPayment(ctx, s.encMnemonic, "", hashB64, genesisID)
	if err != nil {
		return "", fmt.Errorf("tendril login: derive address: %w", err)
	}

	var nonceResp struct {
		Nonce string `json:"nonce"`
	}
	if err := s.client.do(ctx, http.MethodGet, "/auth/wallet-nonce?address="+url.QueryEscape(address), "", &nonceResp); err != nil {
		return "", fmt.Errorf("tendril login: nonce: %w", err)
	}

	signed, _, err := s.signer.SignZeroSelfPayment(ctx, s.encMnemonic, nonceResp.Nonce, hashB64, genesisID)
	if err != nil {
		return "", fmt.Errorf("tendril login: sign: %w", err)
	}

	body, _ := json.Marshal(map[string]string{
		"address": address, "nonce": nonceResp.Nonce, "payment": signed,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.client.baseURL+"/auth/wallet-login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("tendril login: %d %s", resp.StatusCode, truncate(raw))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.token = out.Token
	s.mu.Unlock()
	return out.Token, nil
}

// Balance is the shared platform pool's Tendril credit, in atomic USDC units.
func (s *Session) Balance(ctx context.Context) (int64, error) {
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()

	var w struct {
		BalanceAtomic int64 `json:"balanceAtomic"`
	}
	err := s.client.do(ctx, http.MethodGet, "/wallet", token, &w)
	if err == nil {
		return w.BalanceAtomic, nil
	}
	// A 7-day token can expire mid-life; one silent re-login beats surfacing
	// an auth error to a user who is trying to rent a machine.
	if !strings.Contains(err.Error(), "401") && !strings.Contains(err.Error(), "403") {
		return 0, err
	}
	fresh, lerr := s.login(ctx)
	if lerr != nil {
		return 0, lerr
	}
	if err := s.client.do(ctx, http.MethodGet, "/wallet", fresh, &w); err != nil {
		return 0, err
	}
	return w.BalanceAtomic, nil
}
