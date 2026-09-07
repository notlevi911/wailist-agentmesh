// Package tendril is a typed client for the Tendril compute registry
// (https://tendrilregister.007575.xyz). It speaks only Tendril's own HTTP
// surface: the free market/lease endpoints and the wallet session. It never
// makes an x402 payment — paid routes go through the engine's existing relay
// path, which already knows how to sign and settle.
package tendril

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// atomicAmount decodes an atomic USDC amount that Tendril sends as a JSON
// *string* (its docs: `"chargedAtomic": "83334"`, `"balance": "916666"` —
// kept as strings, like every other x402 amount field, so a large value
// never risks silent float precision loss in a naive client). A plain
// `int64` field here failed json.Unmarshal outright on every real release —
// confirmed live 2026-08-04: the DELETE call to Tendril succeeded and
// genuinely stopped the meter, but our own client couldn't parse the
// response, so ReleaseLease returned an error and never recorded the
// release, leaving the AgentMesh row stuck 'active' while Tendril's own
// side had already closed it. Accepts a bare number too, defensively, in
// case a future Tendril response ever sends one unquoted.
type atomicAmount int64

func (a *atomicAmount) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*a = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("atomicAmount: %q: %w", s, err)
	}
	*a = atomicAmount(v)
	return nil
}

type Asset struct {
	ID       string `json:"id"`
	Decimals int    `json:"decimals"`
	Symbol   string `json:"symbol"`
}

type Platform struct {
	PayTo          string `json:"payTo"`
	Network        string `json:"network"`
	Asset          Asset  `json:"asset"`
	FacilitatorURL string `json:"facilitatorUrl"`
	MinTopUpAtomic int64  `json:"minTopUpAtomic"`
	MaxTopUpAtomic int64  `json:"maxTopUpAtomic"`
}

type Node struct {
	ID              string  `json:"id"`
	Label           string  `json:"label"`
	CPUCores        int     `json:"cpuCores"`
	RAMMb           int     `json:"ramMb"`
	GPU             *string `json:"gpu"`
	PricePerHourUSD float64 `json:"pricePerHourUsd"`
	Status          string  `json:"status"`
	PayoutBlocked   bool    `json:"payoutBlocked"`
}

// RateUSDMicrosPerHour converts Tendril's plain-dollar hourly price into the
// USD-micros unit every ledger in this codebase uses. Rounded to nearest so a
// float like 1.5 never lands a micro short.
func (n Node) RateUSDMicrosPerHour() int64 {
	return int64(n.PricePerHourUSD*1e6 + 0.5)
}

type LeaseStatus struct {
	Status            string `json:"status"`
	RateAtomicPerHour int64  `json:"rateAtomicPerHour"`
	ExpiresAt         string `json:"expiresAt"`
}

type ReleaseResult struct {
	UsedSeconds   int64        `json:"usedSeconds"`
	ChargedAtomic atomicAmount `json:"chargedAtomic"`
	Balance       atomicAmount `json:"balance"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) do(ctx context.Context, method, path, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("tendril %s %s: %d %s", method, path, resp.StatusCode, truncate(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300])
	}
	return string(b)
}

func (c *Client) Platform(ctx context.Context) (Platform, error) {
	var p Platform
	err := c.do(ctx, http.MethodGet, "/platform", "", &p)
	return p, err
}

func (c *Client) OnlineNodes(ctx context.Context) ([]Node, error) {
	var wrapper struct {
		Nodes []Node `json:"nodes"`
	}
	if err := c.do(ctx, http.MethodGet, "/explorer", "", &wrapper); err != nil {
		return nil, err
	}
	online := wrapper.Nodes[:0]
	for _, n := range wrapper.Nodes {
		if n.Status == "online" {
			online = append(online, n)
		}
	}
	sort.SliceStable(online, func(i, j int) bool {
		return online[i].PricePerHourUSD < online[j].PricePerHourUSD
	})
	return online, nil
}

func (c *Client) Lease(ctx context.Context, leaseID, leaseToken string) (LeaseStatus, error) {
	var wrapper struct {
		Lease LeaseStatus `json:"lease"`
	}
	err := c.do(ctx, http.MethodGet, "/lease/"+leaseID, leaseToken, &wrapper)
	return wrapper.Lease, err
}

// Release stops the meter and is where Tendril actually bills the elapsed
// compute. Free despite the /x402/ prefix — no payment is attached.
func (c *Client) Release(ctx context.Context, leaseID, leaseToken string) (ReleaseResult, error) {
	var r ReleaseResult
	err := c.do(ctx, http.MethodDelete, "/x402/leases/"+leaseID, leaseToken, &r)
	return r, err
}
