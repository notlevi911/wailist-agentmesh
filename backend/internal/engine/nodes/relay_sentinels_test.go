package nodes

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestNoPlatformSpendWalletMessageMatchesTheFrozenReturn keeps
// ErrMsgNoPlatformSpendWallet identical to the literal executeTool402V2Relay
// actually returns in its "cannot pay" response body.
//
// tool402.go is byte-frozen, so the literal lives there and this exported
// constant is what other packages (the console handlers) match on. If someone
// rewords the frozen return, this fails and points at the constant to update —
// which is the drift the console's relayUnpayable helper would otherwise hit
// silently.
func TestNoPlatformSpendWalletMessageMatchesTheFrozenReturn(t *testing.T) {
	src, err := os.ReadFile("tool402.go")
	if err != nil {
		t.Fatalf("reading tool402.go: %v", err)
	}
	if !strings.Contains(string(src), strconv.Quote(ErrMsgNoPlatformSpendWallet)) {
		t.Fatalf("tool402.go no longer contains %q verbatim — executeTool402V2Relay's "+
			"cannot-pay message was reworded; update ErrMsgNoPlatformSpendWallet to match",
			ErrMsgNoPlatformSpendWallet)
	}
}
