package service

import "github.com/QuantumNous/new-api/model"

// TaskResultResponseMetadata projects explicit outputs into the video protocol's
// metadata field. It must be built from the adapter result before retention can
// discard it. Never expose raw taskData or provider-supplied content_url values;
// authenticated artifact proxy links are generated only by the owner API.
func TaskResultResponseMetadata(raw []byte, resultURL string) map[string]any {
	result := ValidatedTaskResultMetadata(CollectTaskResultMetadata(raw, resultURL))
	if len(result.Artifacts) == 0 && result.Text == "" {
		return nil
	}
	metadata := make(map[string]any)
	if len(result.Artifacts) > 0 {
		metadata["artifacts"] = result.Artifacts
		// Keep the primary URL convention used by Task.ToOpenAIVideo without
		// substituting a new task proxy that would require a retained result.
		metadata["url"] = result.Artifacts[0].URL
	}
	if result.Text != "" {
		metadata["text"] = result.Text
	}
	return metadata
}

// ApplyImmediateTaskResultRetention runs only on a new submit-time terminal task,
// after ApplyTaskSubmitMetadata has evaluated usage and frozen BillingQuota.
// It changes the row to be inserted, never the adapter's response or result bytes.
// Asynchronous tasks and rows already persisted remain unaffected.
func ApplyImmediateTaskResultRetention(task *model.Task) bool {
	if task == nil || task.ID != 0 || task.BillingState != model.TaskBillingPending || task.BillingQuota < 0 {
		return false
	}
	if task.Status != model.TaskStatusSuccess && task.Status != model.TaskStatusFailure {
		return false
	}
	billing := task.PrivateData.BillingContext
	if billing == nil || billing.UsageSnapshot == nil || billing.UsageSnapshot.RetainResult == nil || *billing.UsageSnapshot.RetainResult {
		return false
	}

	task.PrivateData.ResultDiscarded = true
	task.Data = nil
	task.FailReason = ""
	task.PrivateData.ResultURL = ""
	task.PrivateData.ResultMetadata = nil
	task.PrivateData.ScriptPluginState = ""
	// A terminal task no longer needs provider credentials or a re-fetch ID.
	// Billing identities and the frozen usage definition are preserved.
	task.PrivateData.Key = ""
	task.PrivateData.UpstreamTaskID = ""
	return true
}
