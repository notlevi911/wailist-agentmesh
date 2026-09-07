package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/respond"
	"github.com/agentmesh/backend/internal/wallet"
)

// ListLeases returns the current user's active leases only, never another
// user's and never the encrypted secrets a lease row carries — those stay
// server-side (models.TendrilLease already tags them json:"-").
func (d *Deps) ListLeases(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	leases, err := d.Store.ListActiveTendrilLeases(r.Context(), userID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "failed to list leases")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"leases": leases})
}

// ReleaseLease stops the meter on one of the current user's own leases.
func (d *Deps) ReleaseLease(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	lease, err := d.Store.GetTendrilLease(r.Context(), chi.URLParam(r, "id"))
	if err != nil || lease.UserID != userID {
		respond.Error(w, http.StatusNotFound, "lease not found")
		return
	}
	if d.TendrilClient == nil {
		respond.Error(w, http.StatusServiceUnavailable, "tendril is not configured on this server")
		return
	}
	// Without this, a lease already released (double-click, the reaper
	// beating this request, or Tendril's own watchdog closing it
	// independently of our row) falls into nodes.ReleaseLease's 404
	// fallback, which returns a zero-valued ReleaseResult with no error —
	// so charged/refunded below would compute a phantom "refunded $X.XX"
	// for a release that already happened (and was already correctly
	// refunded) or, worse, never got refunded at all if Tendril's watchdog
	// closed it independently. Confirmed via ultrareview 2026-08-05.
	if lease.Status != "active" {
		respond.Error(w, http.StatusConflict, "lease already released")
		return
	}
	res, refunded, err := nodes.ReleaseLease(r.Context(), nodes.TendrilConfig{
		Client: d.TendrilClient, Store: d.Store, EncryptKey: d.EncryptionKey,
	}, lease)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "release failed: "+err.Error())
		return
	}
	// refunded is exactly what nodes.ReleaseLease actually credited back to
	// the user's Tendril balance -- NOT reservedUSDMicros-charged
	// recomputed here, which would be wrong on the 404 (no real charge
	// data) and not-transitioned (this call refunded nothing; some other
	// caller already handled it, or didn't) paths. See ReleaseLease's own
	// doc comment.
	charged := int64(res.ChargedAtomic)
	respond.JSON(w, http.StatusOK, map[string]any{
		"usedSeconds": res.UsedSeconds,
		"charged":     charged,
		"refunded":    refunded,
		// Deliberately NOT res.Balance: that is the shared pool, which is
		// every user's money and must never be shown to one of them.
	})
}

// DownloadLeaseKey serves the per-lease SSH private key. It must return 404,
// not 403, on a lease that exists but belongs to someone else — a 403 would
// confirm the id exists at all, making lease ids enumerable.
func (d *Deps) DownloadLeaseKey(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	lease, err := d.Store.GetTendrilLease(r.Context(), chi.URLParam(r, "id"))
	if err != nil || lease.UserID != userID {
		respond.Error(w, http.StatusNotFound, "lease not found")
		return
	}
	key, err := wallet.Decrypt(lease.SSHPrivateKeyEnc, d.EncryptionKey)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "key unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "agentmesh-"+lease.LeaseID))
	w.Write([]byte(key))
}

// TendrilMachines lists the current online market, cheapest first — the same
// data the canvas's Rent-node machine picker reads.
func (d *Deps) TendrilMachines(w http.ResponseWriter, r *http.Request) {
	if d.TendrilClient == nil {
		respond.Error(w, http.StatusServiceUnavailable, "tendril is not configured on this server")
		return
	}
	machines, err := d.TendrilClient.OnlineNodes(r.Context())
	if err != nil {
		respond.Error(w, http.StatusBadGateway, "failed to reach the tendril registry")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"machines": machines})
}

// TendrilCredits returns only the requesting user's own Tendril credit
// balance and ledger. It must never expose the shared Wallet 2 pool balance:
// that is the sum of every user's money, and showing it to one user both
// leaks aggregate business data and invites them to believe they can spend it.
func (d *Deps) TendrilCredits(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	balance, err := d.Store.TendrilCreditBalance(r.Context(), userID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "failed to read tendril credit balance")
		return
	}
	recent, err := d.Store.RecentTendrilCreditLedger(r.Context(), userID, 20)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "failed to read tendril credit history")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"tendrilCreditUsdMicros": balance,
		"recent":                 recent,
	})
}

// LeaseTerminal bridges a browser WebSocket to an SSH shell on the rented
// machine, authenticating with the per-lease key AgentMesh generated and
// authorized at rent time.
//
// HostKeyCallback is InsecureIgnoreHostKey deliberately: Tendril hands out a
// fresh ephemeral host (bore.pub tunnels on rotating ports) whose host key we
// have never seen and have no channel to pin. The connection is still
// encrypted; what is not proven is the far end's identity. Revisit if Tendril
// ever publishes host keys in the rent response.
func (d *Deps) LeaseTerminal(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)
	lease, err := d.Store.GetTendrilLease(r.Context(), chi.URLParam(r, "id"))
	if err != nil || lease.UserID != userID || lease.Status != "active" {
		respond.Error(w, http.StatusNotFound, "lease not found")
		return
	}
	keyPEM, err := wallet.Decrypt(lease.SSHPrivateKeyEnc, d.EncryptionKey)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "key unavailable")
		return
	}
	signer, err := ssh.ParsePrivateKey([]byte(keyPEM))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "key unusable")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{originHost(d.FrontendURL)},
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	auth := []ssh.AuthMethod{ssh.PublicKeys(signer)}
	if lease.SSHPasswordEnc != "" {
		if pw, derr := wallet.Decrypt(lease.SSHPasswordEnc, d.EncryptionKey); derr == nil {
			auth = append(auth, ssh.Password(pw))
		}
	}
	client, err := ssh.Dial("tcp",
		fmt.Sprintf("%s:%d", lease.SSHHost, lease.SSHPort),
		&ssh.ClientConfig{
			User:            lease.SSHUsername,
			Auth:            auth,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         20 * time.Second,
		})
	if err != nil {
		conn.Close(websocket.StatusInternalError, "ssh dial failed")
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		conn.Close(websocket.StatusInternalError, "ssh session failed")
		return
	}
	defer session.Close()

	stdin, _ := session.StdinPipe()
	stdout, _ := session.StdoutPipe()
	stderr, _ := session.StderrPipe()
	if err := session.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{
		ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		conn.Close(websocket.StatusInternalError, "pty request failed")
		return
	}
	if err := session.Shell(); err != nil {
		conn.Close(websocket.StatusInternalError, "shell failed")
		return
	}

	ctx := r.Context()
	pump := func(src io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	go pump(stdout)
	go pump(stderr)

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			var msg struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				session.WindowChange(msg.Rows, msg.Cols)
				continue
			}
		}
		if _, err := stdin.Write(data); err != nil {
			return
		}
	}
}

// originHost extracts the host:port a browser will send as Origin, so the
// WebSocket accept is not left origin-wildcarded.
func originHost(frontendURL string) string {
	if u, err := url.Parse(frontendURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "localhost:3000"
}
