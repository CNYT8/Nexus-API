package codex

import (
	"slices"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var baseModelList = []string{
	"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	"gpt-5.5", "gpt-5.4", "gpt-5.4-mini",
	"gpt-5.3-codex-spark", "codex-auto-review",
	"gpt-5", "gpt-5-codex", "gpt-5-codex-mini",
	"gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-max", "gpt-5.1-codex-mini",
	"gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex",
}

var ModelList = slices.DeleteFunc(
	ratio_setting.WithCompactModelVariants(baseModelList),
	func(modelName string) bool {
		return modelName == ratio_setting.WithCompactModelSuffix("codex-auto-review")
	},
)

const ChannelName = "codex"
