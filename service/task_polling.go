package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/samber/lo"
)

// TaskPollingAdaptor 定义轮询所需的最小适配器接口，避免 service -> relay 的循环依赖
type TaskPollingAdaptor interface {
	Init(info *relaycommon.RelayInfo)
	FetchTask(baseURL string, key string, body map[string]any, proxy string) (*http.Response, error)
	ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error)
	// AdjustBillingOnComplete 在任务到达终态（成功/失败）时由轮询循环调用。
	// 返回正数触发差额结算（补扣/退还），返回 0 保持预扣费金额不变。
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

// GetTaskAdaptorFunc 由 main 包注入，用于获取指定平台的任务适配器。
// 打破 service -> relay -> relay/channel -> service 的循环依赖。
var GetTaskAdaptorFunc func(platform constant.TaskPlatform) TaskPollingAdaptor

// Stateful drivers bind the persisted immutable revision before every query.
// Metadata is then saved by the same status CAS as the task and billing target.
type TaskRevisionPollingAdaptor interface {
	BindTask(context.Context, *model.Task) error
	ApplyPollMetadata(*model.Task)
}

// sweepTimedOutTasks 在主轮询之前独立清理超时任务。
// 每次最多处理 100 条，剩余的下个周期继续处理。
// 使用 per-task CAS (UpdateWithStatus) 防止覆盖被正常轮询已推进的任务。
func sweepTimedOutTasks(ctx context.Context) {
	if constant.TaskTimeoutMinutes <= 0 {
		return
	}
	cutoff := time.Now().Unix() - int64(constant.TaskTimeoutMinutes)*60
	tasks := model.GetTimedOutUnfinishedTasks(cutoff, 100, canPollTask)
	if len(tasks) == 0 {
		return
	}

	const legacyTaskCutoff int64 = 1740182400 // 2025-02-22 00:00:00 UTC
	reason := fmt.Sprintf("任务超时（%d分钟）", constant.TaskTimeoutMinutes)
	legacyReason := "任务超时（旧系统遗留任务，不进行退款，请联系管理员）"
	now := time.Now().Unix()
	timedOutCount := 0

	for _, task := range tasks {
		isLegacy := task.SubmitTime > 0 && task.SubmitTime < legacyTaskCutoff

		oldStatus := task.Status
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FinishTime = now
		if isLegacy {
			task.FailReason = legacyReason
			task.Quota = 0
			task.BillingState = model.TaskBillingSkipped
		} else {
			task.FailReason = reason
		}

		won, err := task.UpdateWithStatus(oldStatus)
		if errors.Is(err, model.ErrTaskExecutionProtected) {
			continue
		}
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("sweepTimedOutTasks CAS update error for task %s: %v", task.TaskID, err))
			continue
		}
		if !won {
			logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: task %s already transitioned, skip", task.TaskID))
			continue
		}
		timedOutCount++
		if !isLegacy {
			RefundTaskQuota(ctx, task, reason)
		}
	}

	if timedOutCount > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: timed out %d tasks", timedOutCount))
	}
}

// TaskPollingLoop 主轮询循环，每 15 秒检查一次未完成的任务
func TaskPollingLoop() {
	pollOnce := func() {
		defer EndTaskBillingWork()
		common.SysLog("任务进度轮询开始")
		ctx := context.TODO()
		sweepTimedOutTasks(ctx)
		allTasks := model.GetAllUnFinishSyncTasks(constant.TaskQueryLimit, canPollTask)
		platformTask := make(map[constant.TaskPlatform][]*model.Task)
		for _, t := range allTasks {
			platformTask[t.Platform] = append(platformTask[t.Platform], t)
		}
		for platform, tasks := range platformTask {
			if len(tasks) == 0 {
				continue
			}
			taskChannelM := make(map[int][]string)
			taskM := make(map[string]*model.Task)
			for _, task := range tasks {
				upstreamID := task.GetUpstreamTaskID()
				if upstreamID == "" {
					// 统计失败的未完成任务
					failTaskAndRefund(ctx, task, "任务缺少上游 ID")
					continue
				}
				taskM[upstreamID] = task
				taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], upstreamID)
			}
			if len(taskChannelM) == 0 {
				continue
			}

			DispatchPlatformUpdate(platform, taskChannelM, taskM)
		}
		common.SysLog("任务进度轮询完成")
	}
	for {
		time.Sleep(15 * time.Second)
		if BeginTaskBillingWork() {
			pollOnce()
		}
	}
}

func canPollTask(task *model.Task) bool {
	return task != nil && !task.HasForeignExecutionEvidence() &&
		GetTaskAdaptorFunc != nil && GetTaskAdaptorFunc(task.Platform) != nil
}

func pollableTaskIDs(ids []string, tasks map[string]*model.Task) []string {
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if canPollTask(tasks[id]) {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

// DispatchPlatformUpdate 按平台分发轮询更新
func DispatchPlatformUpdate(platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) {
	switch platform {
	case constant.TaskPlatformMidjourney:
		// MJ 轮询由其自身处理，这里预留入口
	case constant.TaskPlatformSuno:
		_ = UpdateSunoTasks(context.Background(), taskChannelM, taskM)
	default:
		if err := UpdateVideoTasks(context.Background(), platform, taskChannelM, taskM); err != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTasks fail: %s", err))
		}
	}
}

// UpdateSunoTasks 按渠道更新所有 Suno 任务
func UpdateSunoTasks(ctx context.Context, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	for channelId, taskIds := range taskChannelM {
		err := updateSunoTasks(ctx, channelId, taskIds, taskM)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("渠道 #%d 更新异步任务失败: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateSunoTasks(ctx context.Context, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	taskIds = pollableTaskIDs(taskIds, taskM)
	logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
	if len(taskIds) == 0 {
		return nil
	}
	ch, err := model.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CacheGetChannel: %v", err))
		for _, upstreamID := range taskIds {
			recordPollFailure(ctx, taskM[upstreamID], "channel", 0)
		}
		return err
	}
	adaptor := GetTaskAdaptorFunc(constant.TaskPlatformSuno)
	if adaptor == nil {
		return nil
	}
	proxy := ch.GetSetting().Proxy
	resp, err := adaptor.FetchTask(*ch.BaseURL, ch.Key, map[string]any{
		"ids": taskIds,
	}, proxy)
	if err != nil {
		recordPollFailures(ctx, taskIds, taskM, "transport", 0)
		return err
	}
	if resp == nil || resp.Body == nil {
		recordPollFailures(ctx, taskIds, taskM, "transport", 0)
		return errors.New("empty task response")
	}
	defer resp.Body.Close()
	if class := classifyPollHTTP(resp.StatusCode); class != "ok" {
		recordPollFailures(ctx, taskIds, taskM, class, resp.StatusCode)
		return fmt.Errorf("Get Task status code: %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		recordPollFailures(ctx, taskIds, taskM, "transport", resp.StatusCode)
		return err
	}
	var responseItems dto.TaskResponse[[]dto.SunoDataResponse]
	err = common.Unmarshal(responseBody, &responseItems)
	if err != nil {
		recordPollFailures(ctx, taskIds, taskM, "unrecognized", resp.StatusCode)
		return err
	}
	if !responseItems.IsSuccess() {
		recordPollFailures(ctx, taskIds, taskM, "unrecognized", resp.StatusCode)
		return errors.New("upstream task query was not successful")
	}

	seen := make(map[string]bool)
	for _, responseItem := range responseItems.Data {
		task := taskM[responseItem.TaskID]
		if task == nil || task.HasForeignExecutionEvidence() || seen[responseItem.TaskID] {
			continue
		}
		seen[responseItem.TaskID] = true
		if !knownPollStatus(model.TaskStatus(responseItem.Status)) {
			recordPollFailure(ctx, task, "unrecognized", resp.StatusCode)
			continue
		}
		if !taskNeedsUpdate(task, responseItem) {
			continue
		}

		previousStatus := task.Status
		task.PrivateData.PollFailures, task.PrivateData.LastPollClass = 0, "ok"
		task.Status = lo.If(model.TaskStatus(responseItem.Status) != "", model.TaskStatus(responseItem.Status)).Else(task.Status)
		task.FailReason = lo.If(responseItem.FailReason != "", responseItem.FailReason).Else(task.FailReason)
		task.SubmitTime = lo.If(responseItem.SubmitTime != 0, responseItem.SubmitTime).Else(task.SubmitTime)
		task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
		task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
		isFailure := responseItem.FailReason != "" || task.Status == model.TaskStatusFailure
		if isFailure {
			logger.LogInfo(ctx, task.TaskID+" 构建失败，"+task.FailReason)
			task.Status = model.TaskStatusFailure
			task.Progress = "100%"
		}
		if responseItem.Status == model.TaskStatusSuccess {
			task.Progress = "100%"
		}
		task.Data = responseItem.Data
		if task.Status == model.TaskStatusSuccess {
			task.PrivateData.ResultMetadata = CollectTaskResultMetadata(task.Data, task.GetResultURL())
			if quota, _, ok := TaskUsageBillingTarget(task); ok {
				task.BillingState, task.BillingQuota = model.TaskBillingPending, quota
			}
		}

		won, err := task.UpdateWithStatus(previousStatus)
		if errors.Is(err, model.ErrTaskExecutionProtected) {
			continue
		}
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("UpdateSunoTask task %s error: %v", task.TaskID, err))
		} else if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Task %s CAS lost or no-op update, skip billing", task.TaskID))
		} else if isFailure {
			RefundTaskQuota(ctx, task, task.FailReason)
		} else if task.Status == model.TaskStatusSuccess {
			RecalculateTaskQuota(ctx, task, task.BillingQuota, "Suno 预扣额度结算")
		}
	}
	for _, taskID := range taskIds {
		if !seen[taskID] {
			recordPollFailure(ctx, taskM[taskID], "not_found", resp.StatusCode)
		}
	}
	return nil
}

// taskNeedsUpdate 检查 Suno 任务是否需要更新
func taskNeedsUpdate(oldTask *model.Task, newTask dto.SunoDataResponse) bool {
	if oldTask == nil {
		return false
	}
	if oldTask.PrivateData.PollFailures != 0 || oldTask.PrivateData.LastPollClass != "ok" {
		return true
	}
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if string(oldTask.Status) != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}

	if (oldTask.Status == model.TaskStatusFailure || oldTask.Status == model.TaskStatusSuccess) && oldTask.Progress != "100%" {
		return true
	}

	oldData, _ := common.Marshal(oldTask.Data)
	newData, _ := common.Marshal(newTask.Data)

	sort.Slice(oldData, func(i, j int) bool {
		return oldData[i] < oldData[j]
	})
	sort.Slice(newData, func(i, j int) bool {
		return newData[i] < newData[j]
	})

	if string(oldData) != string(newData) {
		return true
	}
	return false
}

// UpdateVideoTasks 按渠道更新所有视频任务
func UpdateVideoTasks(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	for channelId, taskIds := range taskChannelM {
		if err := updateVideoTasks(ctx, platform, channelId, taskIds, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateVideoTasks(ctx context.Context, platform constant.TaskPlatform, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	if GetTaskAdaptorFunc == nil || GetTaskAdaptorFunc(platform) == nil {
		return nil
	}
	taskIds = pollableTaskIDs(taskIds, taskM)
	logger.LogInfo(ctx, fmt.Sprintf("Channel #%d pending video tasks: %d", channelId, len(taskIds)))
	if len(taskIds) == 0 {
		return nil
	}
	cacheGetChannel, err := model.CacheGetChannel(channelId)
	if err != nil {
		for _, upstreamID := range taskIds {
			recordPollFailure(ctx, taskM[upstreamID], "channel", 0)
		}
		return fmt.Errorf("CacheGetChannel failed: %w", err)
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if adaptor == nil {
		return nil
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelBaseUrl:  cacheGetChannel.GetBaseURL(),
		ChannelId:       cacheGetChannel.Id,
		ChannelType:     cacheGetChannel.Type,
		ChannelSetting:  cacheGetChannel.GetSetting(),
		HeadersOverride: cacheGetChannel.GetHeaderOverride(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	for _, taskId := range taskIds {
		if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, taskId, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update video task %s: %s", taskId, err.Error()))
		}
		// sleep 1 second between each task to avoid hitting rate limits of upstream platforms
		time.Sleep(1 * time.Second)
	}
	return nil
}

func updateVideoSingleTask(ctx context.Context, adaptor TaskPollingAdaptor, ch *model.Channel, taskId string, taskM map[string]*model.Task) error {
	baseURL := constant.ChannelBaseURLs[ch.Type]
	if ch.GetBaseURL() != "" {
		baseURL = ch.GetBaseURL()
	}
	proxy := ch.GetSetting().Proxy

	task := taskM[taskId]
	if task == nil {
		logger.LogError(ctx, fmt.Sprintf("Task %s not found in taskM", taskId))
		return fmt.Errorf("task %s not found", taskId)
	}
	if task.HasForeignExecutionEvidence() {
		return nil
	}
	revisionAdaptor, scripted := adaptor.(TaskRevisionPollingAdaptor)
	if scripted {
		if err := revisionAdaptor.BindTask(ctx, task); err != nil {
			recordPollFailure(ctx, task, "hook_error", 0)
			return errors.New("script task revision could not be loaded")
		}
	}
	key := ch.Key

	privateData := task.PrivateData
	if privateData.Key != "" {
		key = privateData.Key
	}
	resp, err := adaptor.FetchTask(baseURL, key, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, proxy)
	if err != nil {
		recordPollFailure(ctx, task, "transport", 0)
		return fmt.Errorf("fetchTask failed for task %s: %w", taskId, err)
	}
	if resp == nil || resp.Body == nil {
		recordPollFailure(ctx, task, "transport", 0)
		return errors.New("empty task response")
	}
	defer resp.Body.Close()
	if class := classifyPollHTTP(resp.StatusCode); class != "ok" {
		recordPollFailure(ctx, task, class, resp.StatusCode)
		return fmt.Errorf("task query HTTP status %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		recordPollFailure(ctx, task, "transport", resp.StatusCode)
		return fmt.Errorf("readAll failed for task %s: %w", taskId, err)
	}

	if !scripted {
		logger.LogDebug(ctx, "updateVideoSingleTask response: %s", responseBody)
	}

	snap := task.Snapshot()

	taskResult := &relaycommon.TaskInfo{}
	// try parse as New API response format
	var responseItems dto.TaskResponse[model.Task]
	if !scripted && common.Unmarshal(responseBody, &responseItems) == nil && responseItems.IsSuccess() {
		logger.LogDebug(ctx, "updateVideoSingleTask parsed as new api response format: %+v", responseItems)
		t := responseItems.Data
		taskResult.TaskID = t.TaskID
		taskResult.Status = string(t.Status)
		taskResult.Url = t.GetResultURL()
		taskResult.Progress = t.Progress
		taskResult.Reason = t.FailReason
		task.Data = t.Data
	} else if taskResult, err = adaptor.ParseTaskResult(responseBody); err != nil {
		recordPollFailure(ctx, task, "hook_error", resp.StatusCode)
		return fmt.Errorf("parseTaskResult failed for task %s: %w", taskId, err)
	}
	if taskResult == nil || !knownPollStatus(model.TaskStatus(taskResult.Status)) {
		recordPollFailure(ctx, task, "unrecognized", resp.StatusCode)
		return nil
	}

	task.PrivateData.PollFailures, task.PrivateData.LastPollClass = 0, "ok"
	task.Data = redactVideoResponseBody(responseBody)
	if scripted {
		revisionAdaptor.ApplyPollMetadata(task)
	} else {
		logger.LogDebug(ctx, "updateVideoSingleTask taskResult: %+v", taskResult)
	}

	now := time.Now().Unix()

	shouldRefund := false
	shouldSettle := false
	quota := task.Quota

	task.Status = model.TaskStatus(taskResult.Status)
	switch task.Status {
	case model.TaskStatusNotStart:
		task.Progress = "0%"
	case model.TaskStatusSubmitted:
		task.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		task.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		task.Progress = taskcommon.ProgressInProgress
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case model.TaskStatusSuccess:
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if strings.HasPrefix(taskResult.Url, "data:") {
			// data: URI (e.g. Vertex base64 encoded video) — keep in Data, not in ResultURL
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		} else if taskResult.Url != "" {
			// Direct upstream URL (e.g. Kling, Ali, Doubao, etc.)
			task.PrivateData.ResultURL = taskResult.Url
		} else {
			// No URL from adaptor — construct proxy URL using public task ID
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
		shouldSettle = true
	case model.TaskStatusFailure:
		logger.LogJson(ctx, fmt.Sprintf("Task %s failed", taskId), task)
		task.Status = model.TaskStatusFailure
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(ctx, fmt.Sprintf("Task %s failed: %s", task.TaskID, task.FailReason))
		taskResult.Progress = taskcommon.ProgressComplete
		if quota != 0 {
			shouldRefund = true
		}
	default:
		return fmt.Errorf("unknown task status %s for task %s", taskResult.Status, task.TaskID)
	}
	if taskResult.Progress != "" {
		task.Progress = taskResult.Progress
	}

	var billingReason string
	var billingClamp *common.QuotaClamp
	if shouldSettle {
		task.PrivateData.ResultMetadata = CollectTaskResultMetadata(responseBody, task.GetResultURL())
		task.BillingQuota, billingReason, billingClamp = TaskBillingTarget(adaptor, task, taskResult)
		task.BillingState = model.TaskBillingPending
	}
	isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if errors.Is(err, model.ErrTaskExecutionProtected) {
			return nil
		}
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("UpdateWithStatus failed for task %s: %s", task.TaskID, err.Error()))
			shouldRefund = false
			shouldSettle = false
		} else if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Task %s already transitioned by another process, skip billing", task.TaskID))
			shouldRefund = false
			shouldSettle = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		if _, err := task.UpdateWithStatus(snap.Status); err != nil && !errors.Is(err, model.ErrTaskExecutionProtected) {
			logger.LogError(ctx, fmt.Sprintf("Failed to update task %s: %s", task.TaskID, err.Error()))
		}
	} else {
		// No changes, skip update
		logger.LogDebug(ctx, "No update needed for task %s", task.TaskID)
	}

	if shouldSettle {
		RecalculateTaskQuota(ctx, task, task.BillingQuota, billingReason, billingClamp)
	}
	if shouldRefund {
		RefundTaskQuota(ctx, task, task.FailReason)
	}

	return nil
}

func redactVideoResponseBody(body []byte) []byte {
	var m map[string]any
	if err := common.Unmarshal(body, &m); err != nil {
		return body
	}
	resp, _ := m["response"].(map[string]any)
	if resp != nil {
		delete(resp, "bytesBase64Encoded")
		if v, ok := resp["video"].(string); ok {
			resp["video"] = truncateBase64(v)
		}
		if vs, ok := resp["videos"].([]any); ok {
			for i := range vs {
				if vm, ok := vs[i].(map[string]any); ok {
					delete(vm, "bytesBase64Encoded")
				}
			}
		}
	}
	b, err := common.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}

// settleTaskBillingOnComplete 任务完成时的统一计费调整。
// 优先级：1. adaptor.AdjustBillingOnComplete 返回正数 → 使用 adaptor 计算的额度
//
//  2. taskResult.TotalTokens > 0 → 按 token 重算
//  3. 都不满足 → 预扣额度即最终额度
//
// 每个成功分支都必须调用 RecalculateTaskQuota：账本任务需要终结持久
// reservation（消费来源 lot、释放 hold、一次性生成返利），否则成功任务
// 的 reservation 会在 TTL 后被错误退款。legacy 差额为零时该调用是 no-op。
func settleTaskBillingOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) {
	actual, reason, clamp := TaskBillingTarget(adaptor, task, taskResult)
	RecalculateTaskQuota(ctx, task, actual, reason, clamp)
}

// TaskBillingTarget shares completion pricing between background polling and
// realtime fetch. Persist its target with the terminal CAS before settling;
// recomputing it afterwards can race recovery or a pricing change.
func TaskBillingTarget(adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) (int, string, *common.QuotaClamp) {
	if quota, reason, ok := ScriptTaskUsageBillingTarget(task); ok {
		return quota, reason, nil
	}
	if bc := task.PrivateData.BillingContext; bc != nil && bc.PerCallBilling {
		return task.Quota, "按次计费结算", nil
	}
	if actual := adaptor.AdjustBillingOnComplete(task, taskResult); actual > 0 {
		return actual, "adaptor计费调整", nil
	}
	if taskResult.TotalTokens > 0 {
		return taskQuotaByTokens(task, taskResult.TotalTokens)
	}
	return task.Quota, "预扣额度即最终额度", nil
}

func classifyPollHTTP(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "ok"
	case code == http.StatusNotFound || code == http.StatusGone:
		return "not_found"
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "auth"
	case code == http.StatusTooManyRequests || code >= 500:
		return "transient"
	case code >= 400 && code < 500:
		return "other_client"
	default:
		return "transient"
	}
}

func knownPollStatus(status model.TaskStatus) bool {
	switch status {
	case model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued, model.TaskStatusInProgress, model.TaskStatusSuccess, model.TaskStatusFailure:
		return true
	default:
		return false
	}
}

func recordPollFailure(ctx context.Context, task *model.Task, class string, code int) {
	if task == nil {
		return
	}
	terminal, err := model.RecordTaskPollFailure(task, class, operation_setting.GetTaskPollMaxFailures())
	if errors.Is(err, model.ErrTaskExecutionProtected) {
		return
	}
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("persist poll failure for task %s: %v", task.TaskID, err))
		return
	}
	logger.LogWarn(ctx, fmt.Sprintf("task %s poll %s (failures=%d, http=%d)", task.TaskID, class, task.PrivateData.PollFailures, code))
	if terminal {
		RefundTaskQuota(ctx, task, task.FailReason)
	}
}

func recordPollFailures(ctx context.Context, ids []string, tasks map[string]*model.Task, class string, code int) {
	for _, id := range ids {
		recordPollFailure(ctx, tasks[id], class, code)
	}
}

func failTaskAndRefund(ctx context.Context, task *model.Task, reason string) {
	if task == nil || task.HasForeignExecutionEvidence() {
		return
	}
	previous := task.Status
	task.Status, task.Progress, task.FailReason = model.TaskStatusFailure, "100%", reason
	won, err := task.UpdateWithStatus(previous)
	if errors.Is(err, model.ErrTaskExecutionProtected) {
		return
	}
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("fail task %d: %v", task.ID, err))
	} else if won {
		RefundTaskQuota(ctx, task, reason)
	}
}
