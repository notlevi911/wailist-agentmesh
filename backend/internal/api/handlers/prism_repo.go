package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentmesh/backend/internal/engine/nodes"
	"github.com/agentmesh/backend/internal/models"
	"github.com/agentmesh/backend/internal/prism"
	"github.com/agentmesh/backend/internal/respond"
)

// Repo-wide code review.
//
// PRISM reviews one file per call, so reviewing a repository is N calls. The
// shape of this feature is dictated by that being real money:
//
//   - Listing is free and separate from reviewing. The user sees the file list
//     and the total before anything is charged.
//   - AgentMesh's flat markup is charged ONCE for the run, not once per file
//     (see nodes.X402RelayConfig.BatchPlatformFee). At $1.50 a call, a 30-file
//     review would otherwise cost $48 for $3 of actual review.
//   - A per-file failure does not abort the run. Files already reviewed were
//     paid for and their results are worth returning.

// maxRepoReviewFiles bounds one run. Not a technical limit — a blast radius.
// At PRISM's price this is still $12 of vendor cost, and a user who pastes a
// large monorepo should be told to narrow it down rather than handed a bill.
const maxRepoReviewFiles = 120

// githubTreeTimeout bounds the listing call. It is one request to GitHub for
// the whole tree, so this is generous rather than tight.
const githubTreeTimeout = 25 * time.Second

// repoReviewCallTimeout bounds a single file's review. PRISM's accurate tier is
// slow, and a batch of these runs sequentially.
const repoReviewCallTimeout = 3 * time.Minute

var githubHTTPClient = &http.Client{Timeout: githubTreeTimeout}

// escapeGitRef percent-escapes a git ref for use in a GitHub API URL segment,
// segment by segment so slashes in a branch name like "release/1.0" survive as
// path separators rather than being encoded themselves.
func escapeGitRef(ref string) string {
	segs := strings.Split(ref, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// fetchRepoTree lists a repository's files via the GitHub trees API.
//
// Unauthenticated: the endpoint has to be a public repo anyway, because PRISM
// fetches each file itself from raw.githubusercontent.com with no credentials.
// A private repo would list here (if we had a token) and then fail on every
// paid call, which is the worst possible ordering.
func fetchRepoTree(ctx context.Context, ref prism.RepoRef) ([]prism.RepoFile, string, error) {
	branch := ref.Ref
	if branch == "" {
		// No ref in the URL: ask GitHub what the default branch is rather than
		// guessing "main" and 404ing on every repo that still uses "master".
		repoURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(ref.Owner), url.PathEscape(ref.Name))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoURL, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		res, err := githubHTTPClient.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("could not reach GitHub: %w", err)
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			return nil, "", fmt.Errorf("no public repository at github.com/%s/%s — private repositories are not supported, because Prism fetches each file itself", ref.Owner, ref.Name)
		}
		if res.StatusCode == http.StatusForbidden {
			return nil, "", fmt.Errorf("GitHub is rate-limiting us right now. Try again in a few minutes")
		}
		if res.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("GitHub returned %d for that repository", res.StatusCode)
		}
		var meta struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := json.NewDecoder(res.Body).Decode(&meta); err != nil {
			return nil, "", fmt.Errorf("could not read that repository's default branch: %w", err)
		} else if meta.DefaultBranch == "" {
			return nil, "", fmt.Errorf("could not read that repository's default branch")
		}
		branch = meta.DefaultBranch
	}

	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1",
		url.PathEscape(ref.Owner), url.PathEscape(ref.Name), escapeGitRef(branch))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, treeURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := githubHTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("no branch %q in that repository", branch)
	}
	if res.StatusCode == http.StatusForbidden {
		return nil, "", fmt.Errorf("GitHub is rate-limiting us right now. Try again in a few minutes")
	}
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GitHub returned %d listing that repository", res.StatusCode)
	}

	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tree); err != nil {
		return nil, "", fmt.Errorf("could not read that repository's file list: %w", err)
	}

	// GitHub truncates a recursive tree past roughly 100k entries or 7 MB, and
	// says so in the payload. Ignoring that flag means listing part of a
	// monorepo as though it were the whole thing — the user then reviews and
	// pays for "the repo" while whole directories were never offered.
	if tree.Truncated {
		return nil, "", fmt.Errorf("that repository is too large for GitHub to list in one go. Try a smaller repository, or review its files individually")
	}

	files := make([]prism.RepoFile, 0, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type != "blob" {
			continue
		}
		files = append(files, prism.ClassifyFile(e.Path, e.Size))
	}
	return files, branch, nil
}

// PrismRepoFiles lists a repository's reviewable files. Free, and charges
// nothing — the whole point is that the user sees the bill before agreeing.
func (d *Deps) PrismRepoFiles(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "That request could not be read. Refresh the page and try again.")
		return
	}
	ref, err := prism.ParseRepoURL(body.Repo)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	files, branch, err := fetchRepoTree(r.Context(), ref)
	if err != nil {
		respond.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	ref.Ref = branch

	reviewable := prism.Reviewable(files)
	respond.JSON(w, http.StatusOK, map[string]any{
		"owner":            ref.Owner,
		"name":             ref.Name,
		"ref":              branch,
		"files":            files,
		"reviewableCount":  len(reviewable),
		"maxFiles":         maxRepoReviewFiles,
		"platformFeeTotal": models.X402PlatformFeeUSDMicros,
	})
}

// repoReviewResult is one file's outcome. A failure carries its own message and
// does not stop the run — the files already reviewed were paid for.
type repoReviewResult struct {
	Path     string `json:"path"`
	Response any    `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
	CostUSD  int64  `json:"costUsdMicros"`
	TxID     string `json:"txId,omitempty"`
}

// PrismRepoReview reviews the selected files, charging the platform fee once.
func (d *Deps) PrismRepoReview(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(CtxUserID).(string)

	var body struct {
		Repo  string   `json:"repo"`
		Ref   string   `json:"ref"`
		Tier  string   `json:"tier"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "That request could not be read. Refresh the page and try again.")
		return
	}
	ref, err := prism.ParseRepoURL(body.Repo)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Ref != "" {
		ref.Ref = body.Ref
	}
	if ref.Ref == "" {
		respond.Error(w, http.StatusBadRequest, "Pick the repository's files first.")
		return
	}
	if len(body.Paths) == 0 {
		respond.Error(w, http.StatusBadRequest, "Select at least one file to review.")
		return
	}
	if len(body.Paths) > maxRepoReviewFiles {
		respond.Error(w, http.StatusBadRequest,
			fmt.Sprintf("That is %d files. The most one review can cover is %d — narrow it down and run it again.",
				len(body.Paths), maxRepoReviewFiles))
		return
	}

	tier := body.Tier
	if tier == "" {
		tier = prism.TierFast
	}
	endpoint, ok := prism.Lookup(prism.TaskCodeReview + "-" + tier)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "Pick either the quick or the thorough review.")
		return
	}

	wf, err := d.Store.GetOrCreateSystemWorkflow(r.Context(), userID, prismConsoleWorkflowName)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not open the Prism console. Try again in a moment.")
		return
	}
	run, err := d.Store.CreateRun(r.Context(), wf.ID, "prism-repo-review", []byte("{}"))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "Could not start the run. Nothing was charged — try again.")
		return
	}

	ledger := newConsoleBatchPaymentLedger(d.Store, userID, wf.ID, run.ID)
	relay := d.prismRelayConfig(ledger)
	// Each file's call bills the vendor amount only; the flat markup is charged
	// once below, for the run as a whole. See the field's doc comment.
	relay.BatchPlatformFee = true

	// The fee is reserved BEFORE any file is reviewed. Reserving it after would
	// mean a user with just enough credit for the files but not the fee gets
	// every file paid for and then a failure to collect our own markup.
	if err := ledger.Reserve(r.Context(), models.X402PlatformFeeUSDMicros); err != nil {
		d.Store.FinishRun(context.WithoutCancel(r.Context()), run.ID, models.RunStatusFailed)
		if respondConsolePaymentRefusal(w, err) {
			return
		}
		// Any other error here is a real infrastructure failure (DB down, tx
		// conflict), not the user's balance — reporting it as "not enough
		// credit" would send them to top up when nothing about their balance
		// is the problem.
		log.Printf("prism repo review: could not reserve platform fee (user=%s run=%s): %v", userID, run.ID, err)
		respond.Error(w, http.StatusInternalServerError, "Could not start the review right now. Nothing was charged — try again.")
		return
	}
	feeCommitted := false
	defer func() {
		// Release the reservation if the run died before the fee was committed;
		// otherwise the user's balance stays decremented with nothing to show.
		if !feeCommitted {
			ledger.Release(r.Context(), models.X402PlatformFeeUSDMicros)
		}
	}()

	// Real sizes, fetched server-side, so the size guards actually bind.
	//
	// This used to re-classify with a hardcoded size of 1, which can never trip
	// `size > maxReviewableBytes` or `size == 0` — so the only checks that
	// survived were the path and extension ones. A caller could post a path the
	// listing itself had marked "too large to review in one call" and have it
	// accepted, paid for, and returned as a context-truncated review at full
	// price. The sizes are free (one more call to the same tree endpoint) and
	// authoritative, unlike anything the client sends up.
	//
	// A listing failure here is logged but not fatal to the request: the loop
	// below still runs, it just cannot classify any file by size and so skips
	// all of them (fail closed) rather than accepting an unverified size.
	sizes := map[string]int64{}
	if listed, _, err := fetchRepoTree(r.Context(), ref); err == nil {
		for _, f := range listed {
			sizes[f.Path] = f.Size
		}
	} else {
		log.Printf("prism repo review: could not re-list %s for sizes, falling back to path checks only: %v", ref, err)
	}

	results := make([]repoReviewResult, 0, len(body.Paths))
	var vendorTotal int64
	anySettled := false

	for i, p := range body.Paths {
		// Re-classify server-side. The client sends a list it got from us, but
		// a caller could post any path — and an unfiltered one is a paid call
		// on a lockfile or, worse, a traversal out of the repo.
		//
		// An unknown size (the re-list above failed, or the file did not
		// appear in it) fails CLOSED, not open: skip the file rather than
		// guessing a size that passes the guard. A guessed size that happens
		// to pass is exactly the "server accepts whatever the client
		// re-sends" bug this re-classification exists to close.
		size, known := sizes[p]
		if !known {
			results = append(results, repoReviewResult{Path: p, Error: "skipped: could not verify this file's size — try the review again"})
			continue
		}
		if cls := prism.ClassifyFile(p, size); cls.Skip != "" {
			results = append(results, repoReviewResult{Path: p, Error: "skipped: " + cls.Skip})
			continue
		}
		node := models.WorkflowNode{
			// Indexed per file: usage/billing volume metrics count distinct
			// (run_id, node_id) pairs, so a fixed ID here would collapse an
			// entire batch's call count down to one.
			ID:       fmt.Sprintf("prism-repo-%s-%d", endpoint.ID, i),
			Type:     models.NodeTypeTool402,
			Name:     endpoint.Title,
			Endpoint: endpoint.URL(),
			Method:   endpoint.Method,
			CustomParams: []models.CustomParam{
				{Name: "raw_url", Kind: "text", Value: ref.RawURL(p)},
				{Name: "file_path", Kind: "text", Value: p},
			},
		}
		// Carry the endpoint's body shape, exactly as buildPrismNode does for
		// the single-file console. Both code-review endpoints take query
		// parameters today, so this is inert — but if either ever gains a
		// template, hand-building the node without it would pay for every file
		// in the repo and have each one rejected. Cheap insurance against the
		// precise failure this console exists to prevent.
		if endpoint.BodyTemplate != "" {
			node.BodyMode = models.BodyModeJSON
			node.BodyTemplate = endpoint.BodyTemplate
		}

		cctx, cancel := context.WithTimeout(r.Context(), repoReviewCallTimeout)
		out, execErr := nodes.ExecuteTool402V2(cctx, node, consoleRunContext{}, models.AgentWallet{}, nil, relay)
		cancel()

		res := repoReviewResult{Path: p}
		switch {
		case execErr != nil:
			// A blocked balance ends the run: every remaining file would fail
			// the same way, and burning through 100 of them to say so is not
			// useful. Files already done keep their results.
			res.Error = execErr.Error()
			results = append(results, res)
			if isBalanceBlocked(execErr) {
				log.Printf("prism repo review: stopping early, balance blocked (user=%s run=%s after %d files)", userID, run.ID, len(results))
				goto done
			}
		case out.SettledUSDMicros == 0 && relayUnpayable(out.Response):
			// executeTool402V2Relay signals "this server has no spend wallet"
			// through the response BODY with a nil error, so without this it
			// lands in the success branch: every file renders a green tick and
			// the header reads "$0.00 charged" for a review that never
			// happened. PrismConsoleRun answers this with a 503; the batch has
			// to recognise it too, and there is no point continuing — every
			// remaining file would fail identically.
			log.Printf("CRITICAL: prism repo review could not pay (user=%s run=%s): the relay has no platform spend wallet or USDC signer configured",
				userID, run.ID)
			res.Error = "Payments are not set up on this server, so this file was not reviewed. You were not charged."
			results = append(results, res)
			goto done
		default:
			res.Response = out.Response
			res.CostUSD = out.SettledUSDMicros
			res.TxID = out.TxID
			vendorTotal += out.SettledUSDMicros
			if out.SettledUSDMicros > 0 {
				anySettled = true
			}
			results = append(results, res)
		}
	}

done:
	// The markup is charged only if at least one file actually settled. A run
	// where nothing was paid for owes us nothing.
	var feeCharged int64
	if anySettled {
		ledger.Commit(r.Context(), "prism-repo-review", models.X402PlatformFeeUSDMicros, models.DebitKindX402PlatformFee)
		feeCommitted = true
		feeCharged = models.X402PlatformFeeUSDMicros
		d.settleRepoReviewFee(r.Context(), userID, run.ID)
	}

	status := models.RunStatusSuccess
	if !anySettled {
		status = models.RunStatusFailed
	}
	// WithoutCancel for the reason PrismConsoleRun spells out, and more so here:
	// a repo review runs for minutes, so the client having gone away is the
	// LIKELY case rather than the edge one — and by this point the vendor calls
	// and the platform fee have already settled. A row stuck at "running" after
	// the user was charged is the worst outcome available.
	d.Store.FinishRun(context.WithoutCancel(r.Context()), run.ID, status)

	respond.JSON(w, http.StatusOK, map[string]any{
		"repo":                 ref.Owner + "/" + ref.Name,
		"ref":                  ref.Ref,
		"tier":                 tier,
		"results":              results,
		"vendorTotalUsdMicros": vendorTotal,
		"platformFeeUsdMicros": feeCharged,
		"totalUsdMicros":       vendorTotal + feeCharged,
	})
}

// settleRepoReviewFee settles the run's single platform markup on-chain, the
// same Wallet 1 -> Wallet 2 leg executeTool402V2Relay does per call. Best
// effort and detached, for the reasons that path documents at length: the
// caller's ledger already reflects the charge, so a failure here is a treasury
// reconciliation problem rather than a reason to fail a review the user has.
func (d *Deps) settleRepoReviewFee(ctx context.Context, userID, runID string) {
	if d.FacilitatorClient == nil {
		return
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nodes.SelfSettleRetryBudget)
	defer cancel()
	txID, err := nodes.SettlePlatformFee(fctx, nodes.RunPreFundConfig{
		USDCSigner:               d.USDCSigner,
		PlatformSpendEncMnemonic: d.PlatformSpendWalletEncMnemonic,
		Facilitator:              d.FacilitatorClient,
		PlatformWalletAddress:    d.PlatformWalletAddress,
		RelayNetwork:             d.RelayNetwork,
		RelayFeePayer:            d.RelayFeePayer,
		ExpectedAssetID:          d.USDCAssetID,
		FrontendURL:              d.FrontendURL,
	}, models.X402PlatformFeeUSDMicros)
	if err != nil {
		log.Printf("CRITICAL: prism repo review platform fee failed to settle on-chain (user=%s run=%s fee=%d): %v",
			userID, runID, models.X402PlatformFeeUSDMicros, err)
		return
	}
	log.Printf("prism repo review: platform fee settled (user=%s run=%s tx=%s)", userID, runID, txID)
}
