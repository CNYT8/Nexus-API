package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	ModelMonitorSampleStatusSuccess = 1
	ModelMonitorSampleStatusError   = 2

	ModelMonitorSampleSourceChannelTest = "channel_test"

	maxModelMonitorSampleErrorLength = 1024
)

type ModelMonitorSample struct {
	Id               int     `json:"id" gorm:"primaryKey"`
	CreatedAt        int64   `json:"created_at" gorm:"bigint;index:idx_model_monitor_samples_lookup,priority:1"`
	Source           string  `json:"source" gorm:"type:varchar(32);index;default:''"`
	ChannelId        int     `json:"channel_id" gorm:"index;default:0"`
	ModelName        string  `json:"model_name" gorm:"type:varchar(255);index:idx_model_monitor_samples_lookup,priority:2;default:''"`
	Group            string  `json:"group" gorm:"type:varchar(64);index;default:'default'"`
	Status           int     `json:"status" gorm:"index:idx_model_monitor_samples_lookup,priority:3;default:0"`
	PromptTokens     int     `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens int     `json:"completion_tokens" gorm:"default:0"`
	UseTime          float64 `json:"use_time" gorm:"default:0"`
	ErrorMessage     string  `json:"error_message" gorm:"type:text"`
}

type RecordModelMonitorSampleParams struct {
	Source           string
	ChannelId        int
	ModelName        string
	Group            string
	Success          bool
	PromptTokens     int
	CompletionTokens int
	UseTimeSeconds   float64
	ErrorMessage     string
}

func truncateModelMonitorSampleError(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	runes := []rune(message)
	if len(runes) <= maxModelMonitorSampleErrorLength {
		return message
	}
	return string(runes[:maxModelMonitorSampleErrorLength])
}

func clampModelMonitorSampleInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func normalizeModelMonitorSampleGroups(primary string, groups []string) []string {
	seen := make(map[string]struct{}, len(groups)+1)
	normalizedGroups := make([]string, 0, len(groups)+1)
	sourceGroups := groups
	if len(sourceGroups) == 0 {
		sourceGroups = []string{primary}
	}
	for _, group := range sourceGroups {
		group = normalizeModelMonitorGroup(group)
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		normalizedGroups = append(normalizedGroups, group)
	}
	if len(normalizedGroups) == 0 {
		return []string{"default"}
	}
	return normalizedGroups
}

func RecordModelMonitorSamples(params RecordModelMonitorSampleParams, groups []string) error {
	modelName := strings.TrimSpace(params.ModelName)
	if modelName == "" {
		return nil
	}
	source := strings.TrimSpace(params.Source)
	if source == "" {
		source = ModelMonitorSampleSourceChannelTest
	}
	status := ModelMonitorSampleStatusError
	if params.Success {
		status = ModelMonitorSampleStatusSuccess
	}
	useTime := params.UseTimeSeconds
	if useTime < 0 {
		useTime = 0
	}
	createdAt := common.GetTimestamp()
	sampleGroups := normalizeModelMonitorSampleGroups(params.Group, groups)
	samples := make([]ModelMonitorSample, 0, len(sampleGroups))
	for _, group := range sampleGroups {
		samples = append(samples, ModelMonitorSample{
			CreatedAt:        createdAt,
			Source:           source,
			ChannelId:        params.ChannelId,
			ModelName:        modelName,
			Group:            group,
			Status:           status,
			PromptTokens:     clampModelMonitorSampleInt(params.PromptTokens),
			CompletionTokens: clampModelMonitorSampleInt(params.CompletionTokens),
			UseTime:          useTime,
			ErrorMessage:     truncateModelMonitorSampleError(params.ErrorMessage),
		})
	}
	if err := LOG_DB.Create(&samples).Error; err != nil {
		return err
	}
	InvalidateModelMonitorCache()
	return nil
}

func RecordModelMonitorSample(params RecordModelMonitorSampleParams) error {
	return RecordModelMonitorSamples(params, nil)
}
