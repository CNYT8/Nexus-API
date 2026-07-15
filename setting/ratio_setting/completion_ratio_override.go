package ratio_setting

import "sync/atomic"

var completionRatioOverrideEnabled atomic.Bool

func init() {
	completionRatioOverrideEnabled.Store(true)
}

func SetCompletionRatioOverrideEnabled(enabled bool) {
	completionRatioOverrideEnabled.Store(enabled)
	InvalidateExposedDataCache()
}

func IsCompletionRatioOverrideEnabled() bool {
	return completionRatioOverrideEnabled.Load()
}
