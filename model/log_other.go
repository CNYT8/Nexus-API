package model

import "encoding/json"

// Keep unmodified metadata as JSON, not float64: billing counters and IDs can
// exceed JavaScript's safe integer range. Invalid metadata fails closed.
func decodeLogOther(value string) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &fields) != nil || fields == nil {
		return make(map[string]json.RawMessage)
	}
	return fields
}

func encodeLogOther(fields map[string]json.RawMessage) string {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func normalizeLogRejectReason(fields map[string]json.RawMessage) {
	if reason, exists := fields["reject_reason"]; exists {
		admin := decodeLogOther(string(fields["admin_info"]))
		if _, exists := admin["reject_reason"]; !exists {
			admin["reject_reason"] = reason
		}
		fields["admin_info"] = json.RawMessage(encodeLogOther(admin))
		delete(fields, "reject_reason")
	}
}

// These fields have never been part of Nexus's user-facing diagnostic API.
// Keep the stricter Nexus model/channel boundary when adapting upstream scopes.
func stripChannelLogMetadata(fields map[string]json.RawMessage) {
	for _, key := range []string{
		"channel_id", "channel_name", "channel_type", "channel_affinity", "use_channel",
		"is_model_mapped", "is_system_prompt_overwritten", "upstream_model_name",
		"original_model", "original_model_name", "upstream_model",
	} {
		delete(fields, key)
	}
}

func formatUserLogOther(value string) string {
	fields := decodeLogOther(value)
	stripChannelLogMetadata(fields)
	for _, key := range []string{"admin_info", "root_info", "audit_info", "reject_reason", "stream_status"} {
		delete(fields, key)
	}
	return encodeLogOther(fields)
}

// FormatAdminLogs is an API projection only; stored audit/root data is untouched.
func FormatAdminLogs(logs []*Log) {
	formatPrivilegedLogs(logs, false)
}

func FormatRootLogs(logs []*Log) {
	formatPrivilegedLogs(logs, true)
}

func formatPrivilegedLogs(logs []*Log, root bool) {
	for _, log := range logs {
		if log == nil || log.Other == "" {
			continue
		}
		fields := decodeLogOther(log.Other)
		normalizeLogRejectReason(fields)
		if !root {
			delete(fields, "root_info")
		}
		log.Other = encodeLogOther(fields)
	}
}

// SetLogOtherAdminField retains existing map-based writers without migrating
// Nexus's whole billing/logging API to an incompatible upstream builder.
func SetLogOtherAdminField(other map[string]interface{}, key string, value interface{}) {
	if other == nil || key == "" {
		return
	}
	admin, ok := other["admin_info"].(map[string]interface{})
	if !ok {
		admin = make(map[string]interface{})
	}
	admin[key] = value
	other["admin_info"] = admin
}
