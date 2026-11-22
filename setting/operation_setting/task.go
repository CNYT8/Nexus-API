package operation_setting

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type TaskSetting struct {
	PollMaxFailures    int              `json:"poll_max_failures"`
	TokenCountMaxBytes int              `json:"token_count_max_bytes"`
	UsagePlugins       map[string]int64 `json:"usage_plugins"`
}

var taskSetting = TaskSetting{PollMaxFailures: 20, TokenCountMaxBytes: 16 * 1024 * 1024, UsagePlugins: map[string]int64{}}

func init() {
	if value, err := strconv.Atoi(os.Getenv("TASK_POLL_MAX_FAILURES")); err == nil {
		taskSetting.PollMaxFailures = value
	}
	config.GlobalConfig.Register("task_setting", &taskSetting)
}

// Non-positive values disable the failure cutoff, retaining timeout handling.
func GetTaskPollMaxFailures() int {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return taskSetting.PollMaxFailures
}

func GetTokenCountMaxBytes() int {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	if taskSetting.TokenCountMaxBytes <= 0 {
		return 16 * 1024 * 1024
	}
	return taskSetting.TokenCountMaxBytes
}

// Bindings are opt-in and exact model names; zero disables new submissions.
func GetTaskUsagePluginID(model string) int64 {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return taskSetting.UsagePlugins[model]
}

func GetTaskUsagePluginBindings() map[string]int64 {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	bindings := make(map[string]int64, len(taskSetting.UsagePlugins))
	for name, id := range taskSetting.UsagePlugins {
		bindings[name] = id
	}
	return bindings
}

// Zero is accepted for legacy option clients; omitting a key also unbinds it.
func ValidateTaskUsagePluginBindings(bindings map[string]int64) error {
	if bindings == nil || len(bindings) > 256 {
		return errors.New("task usage bindings must be an object with at most 256 models")
	}
	for name, id := range bindings {
		if name == "" || len(name) > 255 || strings.TrimSpace(name) != name || !utf8.ValidString(name) || strings.ContainsAny(name, "\r\n\t\x00") || id < 0 || id > 9007199254740991 {
			return errors.New("invalid task usage model name or plugin id")
		}
	}
	return nil
}
