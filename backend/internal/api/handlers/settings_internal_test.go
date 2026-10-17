package handlers

import (
	"strings"
	"testing"
)

func TestParseSettingsPatch(t *testing.T) {
	for _, tc := range []struct {
		name, body                string
		valid, threshold, ceiling bool
	}{
		{"empty patch", "{}", true, false, false},
		{"threshold", `{"lowBalanceUsdMicros":1234567}`, true, true, false},
		{"zero", `{"lowBalanceUsdMicros":0}`, true, true, false},
		{"ceiling", `{"maxCallSpendUsdMicros":50000}`, true, false, true},
		{"clear ceiling", `{"maxCallSpendUsdMicros":null}`, true, false, true},
		{"both", `{"lowBalanceUsdMicros":0,"maxCallSpendUsdMicros":1000000000}`, true, true, true},
		{"missing body", "", false, false, false},
		{"null", "null", false, false, false},
		{"array", "[]", false, false, false},
		{"extra document", "{}{}", false, false, false},
		{"currency", `{"displayCurrency":"USD"}`, false, false, false},
		{"owner", `{"userId":"another"}`, false, false, false},
		{"null threshold", `{"lowBalanceUsdMicros":null}`, false, false, false},
		{"negative", `{"lowBalanceUsdMicros":-1}`, false, false, false},
		{"unsafe integer", `{"lowBalanceUsdMicros":9007199254740992}`, false, false, false},
		{"decimal", `{"lowBalanceUsdMicros":1.5}`, false, false, false},
		{"string", `{"maxCallSpendUsdMicros":"50000"}`, false, false, false},
		{"floor", `{"maxCallSpendUsdMicros":49999}`, false, false, false},
		{"cap", `{"maxCallSpendUsdMicros":1000000001}`, false, false, false},
		{"overflow", `{"maxCallSpendUsdMicros":9223372036854775808}`, false, false, false},
		{"oversized", strings.Repeat(" ", 4097), false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patch, message := parseSettingsPatch(strings.NewReader(tc.body))
			if (message == "") != tc.valid {
				t.Fatalf("valid=%v: %s", tc.valid, message)
			}
			if tc.valid && ((patch.LowBalanceUSDMicros != nil) != tc.threshold || patch.SetMaxCallSpend != tc.ceiling) {
				t.Fatalf("presence not preserved: %+v", patch)
			}
		})
	}
}
