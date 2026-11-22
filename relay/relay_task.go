package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskjsplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type TaskSubmitResult struct {
	UpstreamTaskID         string
	TaskData               []byte
	Platform               constant.TaskPlatform
	Quota                  int
	UsageSnapshot          *model.TaskUsageSnapshot
	ResultMetadata         *model.TaskResultMetadata
	InstantStatus          model.TaskStatus
	ScriptPluginID         int64
	ScriptPluginDigest     string
	ScriptPluginState      string
	ScriptPluginUsage      string
	ScriptPluginUsageError string
	//PerCallPrice   types.PriceData
}

func noteTaskQuotaClamp(info *relaycommon.RelayInfo, clamp *common.QuotaClamp) {
	if clamp == nil || info == nil {
		return
	}
	if info.QuotaClamp == nil {
		info.QuotaClamp = clamp
	}
}

// ResolveOriginTask 处理基于已有任务的提交（remix / continuation）：
// 查找原始任务、从中提取模型名称、将渠道锁定到原始任务的渠道
// （通过 info.LockedChannel，重试时复用同一渠道并轮换 key），
// 以及提取 OtherRatios（时长、分辨率）。
// 该函数在控制器的重试循环之前调用一次，其结果通过 info 字段和上下文持久化。
func ResolveOriginTask(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	// 检测 remix action
	path := c.Request.URL.Path
	if strings.Contains(path, "/v1/videos/") && strings.HasSuffix(path, "/remix") {
		info.Action = constant.TaskActionRemix
	}

	// 提取 remix 任务的 video_id
	if info.Action == constant.TaskActionRemix {
		videoID := c.Param("video_id")
		if strings.TrimSpace(videoID) == "" {
			return service.TaskErrorWrapperLocal(fmt.Errorf("video_id is required"), "invalid_request", http.StatusBadRequest)
		}
		info.OriginTaskID = videoID
	}

	if info.OriginTaskID == "" {
		return nil
	}

	// 查找原始任务
	originTask, exist, err := model.GetByTaskId(info.UserId, info.OriginTaskID)
	if err != nil {
		return service.TaskErrorWrapper(err, "get_origin_task_failed", http.StatusInternalServerError)
	}
	if !exist {
		return service.TaskErrorWrapperLocal(errors.New("task_origin_not_exist"), "task_not_exist", http.StatusBadRequest)
	}

	// 从原始任务推导模型名称
	if info.OriginModelName == "" {
		if originTask.Properties.OriginModelName != "" {
			info.OriginModelName = originTask.Properties.OriginModelName
		} else if originTask.Properties.UpstreamModelName != "" {
			info.OriginModelName = originTask.Properties.UpstreamModelName
		} else {
			var taskData map[string]interface{}
			_ = common.Unmarshal(originTask.Data, &taskData)
			if m, ok := taskData["model"].(string); ok && m != "" {
				info.OriginModelName = m
			}
		}
	}

	// 锁定到原始任务的渠道（重试时复用同一渠道，轮换 key）
	ch, err := model.GetChannelById(originTask.ChannelId, true)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "channel_not_found", http.StatusBadRequest)
	}
	if ch.Status != common.ChannelStatusEnabled {
		return service.TaskErrorWrapperLocal(errors.New("the channel of the origin task is disabled"), "task_channel_disable", http.StatusBadRequest)
	}
	info.LockedChannel = ch

	if originTask.ChannelId != info.ChannelId {
		key, _, newAPIError := ch.GetNextEnabledKey()
		if newAPIError != nil {
			return service.TaskErrorWrapper(newAPIError, "channel_no_available_key", newAPIError.StatusCode)
		}
		common.SetContextKey(c, constant.ContextKeyChannelKey, key)
		common.SetContextKey(c, constant.ContextKeyChannelType, ch.Type)
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, ch.GetBaseURL())
		common.SetContextKey(c, constant.ContextKeyChannelId, originTask.ChannelId)

		info.ChannelBaseUrl = ch.GetBaseURL()
		info.ChannelId = originTask.ChannelId
		info.ChannelType = ch.Type
		info.ApiKey = key
	}

	// 提取 remix 参数（时长、分辨率 → OtherRatios）
	if info.Action == constant.TaskActionRemix {
		if originTask.PrivateData.BillingContext != nil {
			// 新的 remix 逻辑：直接从原始任务的 BillingContext 中提取 OtherRatios（如果存在）
			for s, f := range originTask.PrivateData.BillingContext.OtherRatios {
				info.PriceData.AddOtherRatio(s, f)
			}
		} else {
			// 旧的 remix 逻辑：直接从 task data 解析 seconds 和 size（如果存在）
			var taskData map[string]interface{}
			_ = common.Unmarshal(originTask.Data, &taskData)
			secondsStr, _ := taskData["seconds"].(string)
			seconds, _ := strconv.Atoi(secondsStr)
			if seconds <= 0 {
				seconds = 4
			}
			// 历史任务数据可能包含未经校验的时长，作为计费乘数前必须钳制
			if seconds > relaycommon.MaxTaskDurationSeconds {
				seconds = relaycommon.MaxTaskDurationSeconds
			}
			sizeStr, _ := taskData["size"].(string)
			info.PriceData.AddOtherRatio("seconds", float64(seconds))
			info.PriceData.AddOtherRatio("size", 1)
			if sizeStr == "1792x1024" || sizeStr == "1024x1792" {
				info.PriceData.AddOtherRatio("size", 1.666667)
			}
		}
	}

	return nil
}

// RelayTaskSubmit 完成 task 提交的全部流程（每次尝试调用一次）：
// 刷新渠道元数据 → 确定 platform/adaptor → 验证请求 →
// 估算计费(EstimateBilling) → 计算价格 → 按本次报价补足预扣 →
// 构建/发送/解析上游请求 → 提交后计费调整(AdjustBillingOnSubmit)。
// 控制器负责 defer Refund 和成功后 Settle。
func RelayTaskSubmit(c *gin.Context, info *relaycommon.RelayInfo) (*TaskSubmitResult, *dto.TaskError) {
	// Distributor has already enforced token/model/group/channel permissions.
	// Resolve each attempt independently because retries can select a new channel.
	info.InitChannelMeta(c)
	binding, plugin, err := service.ResolveScriptPluginBinding(c.Request.Context(), info.ChannelId, info.OriginModelName)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(errors.New("script plugin binding is unavailable"), "script_plugin_unavailable", http.StatusServiceUnavailable)
	}
	if binding != nil {
		return relayTaskSubmitWithAdaptor(c, info, func(constant.TaskPlatform) channel.TaskAdaptor {
			return taskjsplugin.New(binding.PluginID, plugin)
		})
	}
	return relayTaskSubmitWithAdaptor(c, info, GetTaskAdaptor)
}

func relayTaskSubmitWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptorFactory func(constant.TaskPlatform) channel.TaskAdaptor) (*TaskSubmitResult, *dto.TaskError) {
	info.InitChannelMeta(c)

	// 1. 确定 platform → 创建适配器 → 验证请求
	platform := constant.TaskPlatform(c.GetString("platform"))
	if platform == "" {
		platform = GetTaskPlatform(c)
	}
	adaptor := adaptorFactory(platform)
	if adaptor == nil {
		return nil, service.TaskErrorWrapperLocal(fmt.Errorf("invalid api platform: %s", platform), "invalid_api_platform", http.StatusBadRequest)
	}
	if _, scripted := adaptor.(*taskjsplugin.TaskAdaptor); scripted {
		platform = constant.TaskPlatform(taskjsplugin.Platform)
	}
	adaptor.Init(info)
	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		return nil, taskErr
	}

	// 2. 确定模型名称
	modelName := info.OriginModelName
	if modelName == "" {
		modelName = service.CoverTaskActionToModelName(platform, info.Action)
	}

	// 2.5 应用渠道的模型映射（与同步任务对齐）
	info.OriginModelName = modelName
	info.UpstreamModelName = modelName
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "model_mapping_failed", http.StatusBadRequest)
	}

	// 3. 预生成公开 task ID（仅首次）
	if info.PublicTaskID == "" {
		info.PublicTaskID = model.GenerateTaskID()
	}

	// 4. 价格计算：基础模型价格。重试可能换了渠道，每次尝试都重新定价，不沿用上一次尝试的表达式快照。
	info.OriginModelName = modelName
	info.TieredBillingSnapshot = nil
	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "model_price_error", http.StatusBadRequest)
	}
	info.PriceData = priceData

	// 5. 计费估算：让适配器根据用户请求提供 OtherRatios（时长、分辨率等）
	//    必须在 ModelPriceHelperPerCall 之后调用（它会重建 PriceData）。
	//    ResolveOriginTask 可能已在 remix 路径中预设了 OtherRatios，此处合并。
	if estimatedRatios := adaptor.EstimateBilling(c, info); len(estimatedRatios) > 0 {
		for k, v := range estimatedRatios {
			info.PriceData.AddOtherRatio(k, v)
		}
	}

	// 6. 将 OtherRatios 应用到基础额度
	if !common.StringsContains(constant.TaskPricePatches, modelName) {
		quotaWithRatios := float64(info.PriceData.Quota)
		for _, ra := range info.PriceData.OtherRatios {
			if ra != 1.0 {
				quotaWithRatios *= ra
			}
		}
		quota, clamp := common.QuotaFromFloatChecked(quotaWithRatios)
		noteTaskQuotaClamp(info, clamp)
		info.PriceData.Quota = quota
	}

	usageSnapshot, usageErr := service.FreezeTaskUsageBilling(modelName, info.PriceData.GroupRatioInfo.GroupRatio, info.PriceData.OtherRatios)
	if usageErr != nil {
		return nil, service.TaskErrorWrapperLocal(usageErr, "task_usage_plugin_invalid", http.StatusBadRequest)
	}
	if script, ok := adaptor.(*taskjsplugin.TaskAdaptor); ok {
		quote, hasUsage, err := script.FreezeUsageBilling(usageSnapshot)
		if err != nil {
			return nil, service.TaskErrorWrapperLocal(err, "script_usage_invalid", http.StatusBadRequest)
		}
		if hasUsage {
			// Keep the host quote as a reservation floor; final zero usage still settles to zero.
			info.PriceData.Quota = max(info.PriceData.Quota, quote)
		}
	}
	if usageSnapshot != nil && info.PriceData.FreeModel {
		return nil, service.TaskErrorWrapperLocal(errors.New("usage billing requires a non-free task reservation price"), "task_usage_plugin_invalid", http.StatusBadRequest)
	}

	// 7. 每次尝试均校验报价；已有会话不能绕过溢出保护。
	if info.QuotaClamp != nil {
		taskErr := service.TaskErrorFromAPIError(service.TaskWalletQuotaClampError(info.QuotaClamp))
		taskErr.LocalError = true
		return nil, taskErr
	}
	if info.Billing == nil && !info.PriceData.FreeModel {
		info.ForcePreConsume = true
		// Session pre-consumption persists funding mode and token together,
		// before upstream I/O or task insertion. Controller refunds through
		// info.Billing and therefore cannot refund the token a second time.
		if c.GetString(service.ContextKeyTaskWalletBillingKey) == "" {
			if taskErr := createLedgerTaskSubscriptionSession(c, info); taskErr != nil {
				return nil, taskErr
			}
		}
	}

	// 8. 构建请求体
	requestBody, err := adaptor.BuildRequestBody(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "build_request_failed", http.StatusInternalServerError)
	}

	// 9. 自动跨组重试可能涨价，必须先补足资金与令牌，再标记发送。
	// 降价不会缩减会话预扣；持久化使用真实预扣，最终报价保留在 result。
	if info.Billing != nil {
		reserveErr := info.Billing.Reserve(info.PriceData.Quota)
		reserved := info.Billing.GetPreConsumedQuota()
		info.FinalPreConsumedQuota = reserved
		c.Set(service.ContextKeyTaskWalletBillingQuota, reserved)
		if reserveErr != nil {
			taskErr := service.TaskErrorFromAPIError(service.TaskWalletReserveError(reserveErr))
			taskErr.LocalError = true
			return nil, taskErr
		}
	}
	if dispatchErr := service.MarkBillingDispatched(info); dispatchErr != nil {
		return nil, service.TaskErrorWrapperLocal(dispatchErr, "billing_dispatch_failed", http.StatusInternalServerError)
	}
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	_, scripted := adaptor.(*taskjsplugin.TaskAdaptor)
	// The script adapter accepts all 2xx and bounds/redacts rejected replies.
	if !scripted && resp != nil && resp.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(resp.Body)
		return nil, service.TaskErrorWrapper(fmt.Errorf("%s", string(responseBody)), "fail_to_fetch_task", resp.StatusCode)
	}

	// 10. 返回 OtherRatios 给下游（header 必须在 DoResponse 写 body 之前设置）
	otherRatios := info.PriceData.OtherRatios
	if otherRatios == nil {
		otherRatios = map[string]float64{}
	}
	ratiosJSON, _ := common.Marshal(otherRatios)
	c.Header("X-New-Api-Other-Ratios", string(ratiosJSON))

	// 11. 解析响应
	upstreamTaskID, taskData, taskErr := adaptor.DoResponse(c, resp, info)
	if taskErr != nil {
		return nil, taskErr
	}

	// 11. 提交后计费调整：让适配器根据上游实际返回调整 OtherRatios
	finalQuota := info.PriceData.Quota
	if script, ok := adaptor.(*taskjsplugin.TaskAdaptor); ok {
		finalQuota = script.UsageQuote(finalQuota)
		if info.Billing != nil && finalQuota > info.Billing.GetPreConsumedQuota() {
			// Submission is already accepted. Preserve the task and its target even
			// if funding cannot grow now; the existing settlement retry owns recovery.
			if err := info.Billing.Reserve(finalQuota); err != nil {
				common.SysError("script usage reservation increase failed; retaining settlement target")
			}
			info.FinalPreConsumedQuota = info.Billing.GetPreConsumedQuota()
			c.Set(service.ContextKeyTaskWalletBillingQuota, info.FinalPreConsumedQuota)
		}
	} else if usageSnapshot == nil {
		if adjustedRatios := adaptor.AdjustBillingOnSubmit(info, taskData); len(adjustedRatios) > 0 {
			// 基于调整后的 ratios 重新计算 quota
			finalQuota = recalcQuotaFromRatios(info, adjustedRatios)
			info.PriceData.OtherRatios = adjustedRatios
			info.PriceData.Quota = finalQuota
		}
	}

	result := &TaskSubmitResult{
		UpstreamTaskID: upstreamTaskID,
		TaskData:       taskData,
		Platform:       platform,
		Quota:          finalQuota,
		UsageSnapshot:  usageSnapshot,
		ResultMetadata: service.CollectTaskResultMetadata(taskData, ""),
	}
	if script, ok := adaptor.(*taskjsplugin.TaskAdaptor); ok {
		result.ScriptPluginID, result.ScriptPluginDigest, result.ScriptPluginState = script.ScriptSnapshot()
		result.ScriptPluginUsage, result.ScriptPluginUsageError = script.UsageSnapshot()
	}
	if instant, parseErr := adaptor.ParseTaskResult(taskData); parseErr == nil && instant != nil {
		result.ResultMetadata = service.CollectTaskResultMetadata(taskData, instant.Url)
		if instant.Status == model.TaskStatusSuccess || instant.Status == model.TaskStatusFailure {
			result.InstantStatus = model.TaskStatus(instant.Status)
		}
	}
	return result, nil
}

// ApplyTaskSubmitMetadata must run after constructing BillingContext and Data,
// before task.Insert. It persists the immutable policy and immediate final target
// in the same insert as the existing funding identity.
func ApplyTaskSubmitMetadata(task *model.Task, result *TaskSubmitResult) {
	if task == nil || result == nil {
		return
	}
	if result.ScriptPluginID > 0 {
		task.PrivateData.ScriptPluginID = result.ScriptPluginID
		task.PrivateData.ScriptPluginDigest = result.ScriptPluginDigest
		task.PrivateData.ScriptPluginState = result.ScriptPluginState
		task.PrivateData.ScriptPluginUsage = result.ScriptPluginUsage
		task.PrivateData.ScriptPluginUsageError = result.ScriptPluginUsageError
	}
	if result.UsageSnapshot != nil {
		if task.PrivateData.BillingContext == nil {
			task.PrivateData.BillingContext = &model.TaskBillingContext{}
		}
		task.PrivateData.BillingContext.UsageSnapshot = result.UsageSnapshot
	}
	if result.ResultMetadata != nil {
		task.PrivateData.ResultMetadata = result.ResultMetadata
	}
	if result.InstantStatus != model.TaskStatusSuccess && result.InstantStatus != model.TaskStatusFailure {
		return
	}
	task.Status, task.Progress, task.FinishTime = result.InstantStatus, "100%", common.GetTimestamp()
	task.BillingState, task.BillingQuota = model.TaskBillingPending, result.Quota
	if task.Status == model.TaskStatusFailure {
		task.BillingQuota = 0
	} else if quota, _, ok := service.ScriptTaskUsageBillingTarget(task); ok {
		task.BillingQuota = quota
	}
}

// createLedgerTaskSubscriptionSession creates the common atomic submit session.
// Both funding sources transfer their reservation to task persistence.
// The factory retains the user's subscription/wallet fallback preference.
func createLedgerTaskSubscriptionSession(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	session, apiErr := service.NewBillingSession(c, info, info.PriceData.Quota)
	if apiErr != nil {
		taskErr := service.TaskErrorFromAPIError(apiErr)
		taskErr.LocalError = true
		return taskErr
	}
	info.Billing = session
	key := session.TaskReservationKey()
	if key == "" {
		return service.TaskErrorWrapperLocal(service.ErrWalletBillingIdentityRequired, "billing_identity_failed", http.StatusInternalServerError)
	}
	c.Set(service.ContextKeyTaskWalletBillingKey, key)
	info.FinalPreConsumedQuota = session.GetPreConsumedQuota()
	c.Set(service.ContextKeyTaskWalletBillingQuota, info.FinalPreConsumedQuota)
	return nil
}

// recalcQuotaFromRatios 根据 adjustedRatios 重新计算 quota。
// 公式: baseQuota × ∏(ratio) — 其中 baseQuota 是不含 OtherRatios 的基础额度。
func recalcQuotaFromRatios(info *relaycommon.RelayInfo, ratios map[string]float64) int {
	// 从 PriceData 获取不含 OtherRatios 的基础价格
	baseQuota := float64(info.PriceData.Quota)
	// 先除掉原有的 OtherRatios 恢复基础额度
	for _, ra := range info.PriceData.OtherRatios {
		if ra != 1.0 && ra > 0 {
			baseQuota /= ra
		}
	}
	// 应用新的 ratios
	result := baseQuota
	for _, ra := range ratios {
		if ra != 1.0 {
			result *= ra
		}
	}
	quota, clamp := common.QuotaFromFloatChecked(result)
	noteTaskQuotaClamp(info, clamp)
	return quota
}

var fetchRespBuilders = map[int]func(c *gin.Context) (respBody []byte, taskResp *dto.TaskError){
	relayconstant.RelayModeSunoFetchByID:  sunoFetchByIDRespBodyBuilder,
	relayconstant.RelayModeSunoFetch:      sunoFetchRespBodyBuilder,
	relayconstant.RelayModeVideoFetchByID: videoFetchByIDRespBodyBuilder,
}

func RelayTaskFetch(c *gin.Context, relayMode int) (taskResp *dto.TaskError) {
	respBuilder, ok := fetchRespBuilders[relayMode]
	if !ok {
		taskResp = service.TaskErrorWrapperLocal(errors.New("invalid_relay_mode"), "invalid_relay_mode", http.StatusBadRequest)
	}

	respBody, taskErr := respBuilder(c)
	if taskErr != nil {
		return taskErr
	}
	if len(respBody) == 0 {
		respBody = []byte("{\"code\":\"success\",\"data\":null}")
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	_, err := io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError)
		return
	}
	return
}

func sunoFetchRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	userId := c.GetInt("id")
	var condition = struct {
		IDs    []any  `json:"ids"`
		Action string `json:"action"`
	}{}
	err := c.BindJSON(&condition)
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "invalid_request", http.StatusBadRequest)
		return
	}
	var tasks []any
	if len(condition.IDs) > 0 {
		taskModels, err := model.GetByTaskIds(userId, condition.IDs)
		if err != nil {
			taskResp = service.TaskErrorWrapper(err, "get_tasks_failed", http.StatusInternalServerError)
			return
		}
		for _, task := range taskModels {
			tasks = append(tasks, TaskModel2Dto(task))
		}
	} else {
		tasks = make([]any, 0)
	}
	respBody, err = common.Marshal(dto.TaskResponse[[]any]{
		Code: "success",
		Data: tasks,
	})
	return
}

func sunoFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("id")
	userId := c.GetInt("id")

	originTask, exist, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
		return
	}
	if !exist {
		taskResp = service.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusBadRequest)
		return
	}

	respBody, err = common.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: TaskModel2Dto(originTask),
	})
	return
}

func videoFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("task_id")
	if taskId == "" {
		taskId = c.GetString("task_id")
	}
	userId := c.GetInt("id")

	originTask, exist, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
		return
	}
	if !exist {
		taskResp = service.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusBadRequest)
		return
	}

	isOpenAIVideoAPI := strings.HasPrefix(c.Request.RequestURI, "/v1/videos/")

	// Gemini/Vertex 支持实时查询：用户 fetch 时直接从上游拉取最新状态
	if realtimeResp := tryRealtimeFetch(originTask, isOpenAIVideoAPI); len(realtimeResp) > 0 {
		respBody = realtimeResp
		return
	}

	// OpenAI Video API 格式: 走各 adaptor 的 ConvertToOpenAIVideo
	if isOpenAIVideoAPI {
		adaptor := GetTaskAdaptor(originTask.Platform)
		if adaptor == nil {
			taskResp = service.TaskErrorWrapperLocal(fmt.Errorf("invalid channel id: %d", originTask.ChannelId), "invalid_channel_id", http.StatusBadRequest)
			return
		}
		if converter, ok := adaptor.(channel.OpenAIVideoConverter); ok {
			openAIVideoData, err := converter.ConvertToOpenAIVideo(originTask)
			if err != nil {
				taskResp = service.TaskErrorWrapper(err, "convert_to_openai_video_failed", http.StatusInternalServerError)
				return
			}
			respBody = openAIVideoData
			return
		}
		taskResp = service.TaskErrorWrapperLocal(fmt.Errorf("not_implemented:%s", originTask.Platform), "not_implemented", http.StatusNotImplemented)
		return
	}

	// 通用 TaskDto 格式
	respBody, err = common.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: TaskModel2Dto(originTask),
	})
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return
}

// tryRealtimeFetch 尝试从上游实时拉取 Gemini/Vertex 任务状态。
// 仅当渠道类型为 Gemini 或 Vertex 时触发；其他渠道或出错时返回 nil。
// 当非 OpenAI Video API 时，还会构建自定义格式的响应体。
func tryRealtimeFetch(task *model.Task, isOpenAIVideoAPI bool) []byte {
	return tryRealtimeFetchWithAdaptor(task, isOpenAIVideoAPI, func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		return GetTaskAdaptor(platform)
	})
}

// The factory parameter keeps provider I/O replaceable in regression tests;
// production and tests execute the same terminal CAS and billing path.
func tryRealtimeFetchWithAdaptor(task *model.Task, isOpenAIVideoAPI bool, getAdaptor func(constant.TaskPlatform) service.TaskPollingAdaptor) []byte {
	if task == nil || task.HasForeignExecutionEvidence() || task.PrivateData.ScriptPluginID > 0 || task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		return nil
	}
	// Empty platform is retained for native tasks created before it was stored.
	if getAdaptor == nil || (task.Platform != "" && getAdaptor(task.Platform) == nil) {
		return nil
	}
	channelModel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		return nil
	}
	if channelModel.Type != constant.ChannelTypeVertexAi && channelModel.Type != constant.ChannelTypeGemini {
		return nil
	}

	baseURL := constant.ChannelBaseURLs[channelModel.Type]
	if channelModel.GetBaseURL() != "" {
		baseURL = channelModel.GetBaseURL()
	}
	proxy := channelModel.GetSetting().Proxy
	adaptor := getAdaptor(constant.TaskPlatform(strconv.Itoa(channelModel.Type)))
	if adaptor == nil {
		return nil
	}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: channelModel.Type, ChannelBaseUrl: baseURL, ApiKey: channelModel.Key,
	}})

	resp, err := adaptor.FetchTask(baseURL, channelModel.Key, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, proxy)
	if err != nil || resp == nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	ti, err := adaptor.ParseTaskResult(body)
	if err != nil || ti == nil {
		return nil
	}

	snap := task.Snapshot()
	original := *task

	// 将上游最新状态更新到 task
	if ti.Status != "" {
		task.Status = model.TaskStatus(ti.Status)
	}
	if ti.Progress != "" {
		task.Progress = ti.Progress
	}
	if strings.HasPrefix(ti.Url, "data:") {
		// data: URI — kept in Data, not ResultURL
	} else if ti.Url != "" {
		task.PrivateData.ResultURL = ti.Url
	} else if task.Status == model.TaskStatusSuccess {
		// No URL from adaptor — construct proxy URL using public task ID
		task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
	}

	task.Data = body
	task.PrivateData.PollFailures, task.PrivateData.LastPollClass = 0, "ok"
	task.PrivateData.ResultMetadata = service.CollectTaskResultMetadata(body, task.GetResultURL())
	var billingReason string
	var billingClamp *common.QuotaClamp
	terminal := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if terminal {
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = common.GetTimestamp()
		}
		if task.Status == model.TaskStatusSuccess {
			task.BillingQuota, billingReason, billingClamp = service.TaskBillingTarget(adaptor, task, ti)
		} else {
			task.BillingQuota = 0
			task.FailReason = ti.Reason
		}
		task.BillingState = model.TaskBillingPending
	}
	if !snap.Equal(task.Snapshot()) {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil || !won {
			// Return the committed state, never an uncommitted or losing
			// terminal snapshot. Only the CAS winner may adjust billing.
			*task = original
			if stored, exists, readErr := model.GetByTaskId(task.UserId, task.TaskID); readErr == nil && exists {
				*task = *stored
			}
			if err != nil && !errors.Is(err, model.ErrTaskExecutionProtected) {
				common.SysError(fmt.Sprintf("realtime task %s update failed: %v", task.TaskID, err))
			}
			return nil
		}
		if terminal {
			if task.Status == model.TaskStatusSuccess {
				service.RecalculateTaskQuota(context.Background(), task, task.BillingQuota, billingReason, billingClamp)
			} else {
				service.RefundTaskQuota(context.Background(), task, task.FailReason)
			}
		}
	}

	// OpenAI Video API 由调用者的 ConvertToOpenAIVideo 分支处理
	if isOpenAIVideoAPI {
		return nil
	}

	// 非 OpenAI Video API: 构建自定义格式响应
	format := detectVideoFormat(body)
	out := map[string]any{
		"error":    nil,
		"format":   format,
		"metadata": nil,
		"status":   mapTaskStatusToSimple(task.Status),
		"task_id":  task.TaskID,
		"url":      task.GetResultURL(),
	}
	respBody, _ := common.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: out,
	})
	return respBody
}

// detectVideoFormat 从 Gemini/Vertex 原始响应中探测视频格式
func detectVideoFormat(rawBody []byte) string {
	var raw map[string]any
	if err := common.Unmarshal(rawBody, &raw); err != nil {
		return "mp4"
	}
	respObj, ok := raw["response"].(map[string]any)
	if !ok {
		return "mp4"
	}
	vids, ok := respObj["videos"].([]any)
	if !ok || len(vids) == 0 {
		return "mp4"
	}
	v0, ok := vids[0].(map[string]any)
	if !ok {
		return "mp4"
	}
	mt, ok := v0["mimeType"].(string)
	if !ok || mt == "" || strings.Contains(mt, "mp4") {
		return "mp4"
	}
	return mt
}

// mapTaskStatusToSimple 将内部 TaskStatus 映射为简化状态字符串
func mapTaskStatusToSimple(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusSuccess:
		return "succeeded"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusQueued, model.TaskStatusSubmitted:
		return "queued"
	default:
		return "processing"
	}
}

func TaskModel2Dto(task *model.Task) *dto.TaskDto {
	var usageSnapshot *dto.TaskUsageSnapshot
	if billing := task.PrivateData.BillingContext; billing != nil && billing.UsageSnapshot != nil {
		snapshot := billing.UsageSnapshot
		// Explicitly copy the public definition. Never serialize the private
		// billing snapshot or share its mutable schema with a response DTO.
		schema := make(map[string]dto.TaskUsageField, len(snapshot.Schema))
		for name, field := range snapshot.Schema {
			schema[name] = dto.TaskUsageField{
				Path: field.Path, Required: field.Required, Default: field.Default, Max: field.Max,
			}
		}
		usageSnapshot = &dto.TaskUsageSnapshot{
			Name: snapshot.Name, Version: snapshot.Version, Schema: schema, Expression: snapshot.Expression,
		}
	}
	result := &dto.TaskDto{
		ID:            task.ID,
		CreatedAt:     task.CreatedAt,
		UpdatedAt:     task.UpdatedAt,
		TaskID:        task.TaskID,
		Platform:      string(task.Platform),
		UserId:        task.UserId,
		Group:         task.Group,
		ChannelId:     task.ChannelId,
		Quota:         task.Quota,
		Action:        task.Action,
		Status:        string(task.Status),
		FailReason:    task.FailReason,
		ResultURL:     task.GetResultURL(),
		SubmitTime:    task.SubmitTime,
		StartTime:     task.StartTime,
		FinishTime:    task.FinishTime,
		Progress:      task.Progress,
		Properties:    task.Properties,
		Username:      task.Username,
		Data:          task.Data,
		UsageSnapshot: usageSnapshot,
	}
	if !task.ResultRetrievable() {
		result.ResultDiscarded = true
		result.Data = nil
		result.FailReason = ""
		result.ResultURL = ""
	}
	return result
}
