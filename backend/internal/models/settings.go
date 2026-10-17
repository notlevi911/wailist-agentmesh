package models

const DefaultLowBalanceUSDMicros int64 = 5_000_000

// Keep JSON amounts exactly representable by the browser.
const MaxLowBalanceUSDMicros int64 = 9_007_199_254_740_991

type UserSettings struct {
	LowBalanceUSDMicros   int64  `json:"lowBalanceUsdMicros"`
	MaxCallSpendUSDMicros *int64 `json:"maxCallSpendUsdMicros"`
}

func DefaultUserSettings() UserSettings {
	return UserSettings{LowBalanceUSDMicros: DefaultLowBalanceUSDMicros}
}

type UserSettingsPatch struct {
	LowBalanceUSDMicros *int64
	// An explicit null removes the ceiling; an absent key leaves it alone.
	SetMaxCallSpend       bool
	MaxCallSpendUSDMicros *int64
}
