package nodes

// ErrMsgNoPlatformSpendWallet is the string executeTool402V2Relay puts in its
// response body — paired with a NIL error — when this server has no platform
// spend wallet or USDC signer configured and so cannot pay an x402 challenge.
//
// tool402.go is byte-frozen (engine_test.TestX402PaymentPathIsFrozen), so the
// literal is spelled out there and cannot reference this symbol. This constant
// is the copy the Prism console's relayUnpayable helper matches against
// instead of re-typing the literal (the Tendril console does not run this
// check; it reports a relay failure as a bare 502). relay_sentinels_test.go
// asserts the two stay identical, so a reword of the frozen return fails
// loudly and names this constant.
const ErrMsgNoPlatformSpendWallet = "payment required but no platform spend wallet configured"
