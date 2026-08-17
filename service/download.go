package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// WorkerRequest Worker请求的数据结构
type WorkerRequest struct {
	URL     string            `json:"url"`
	Key     string            `json:"key"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

// DoWorkerRequest 通过Worker发送请求
func DoWorkerRequest(req *WorkerRequest) (*http.Response, error) {
	if !system_setting.EnableWorker() {
		return nil, fmt.Errorf("worker not enabled")
	}
	if !system_setting.WorkerAllowHttpImageRequestEnabled && !strings.HasPrefix(req.URL, "https") {
		return nil, fmt.Errorf("only support https url")
	}

	// Worker最终会自行解析目标，因此API侧至少始终执行强制的非私网/端口校验。
	if err := ValidateUntrustedOutboundURL(req.URL); err != nil {
		return nil, fmt.Errorf("request reject: %v", err)
	}

	workerUrl := system_setting.WorkerUrl
	if !strings.HasSuffix(workerUrl, "/") {
		workerUrl += "/"
	}

	// 序列化worker请求数据
	workerPayload, err := common.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal worker payload: %v", err)
	}

	return GetHttpClient().Post(workerUrl, "application/json", bytes.NewBuffer(workerPayload))
}

func DoDownloadRequest(originUrl string, reason ...string) (resp *http.Response, err error) {
	if system_setting.EnableWorker() {
		common.SysLog(fmt.Sprintf("downloading file from worker: %s, reason: %s", originUrl, strings.Join(reason, ", ")))
		req := &WorkerRequest{
			URL: originUrl,
			Key: system_setting.WorkerValidKey,
		}
		return DoWorkerRequest(req)
	} else {
		// 用户可控下载目标始终启用强制防护，不受root关闭普通SSRF开关影响。
		if err := ValidateUntrustedOutboundURL(originUrl); err != nil {
			return nil, fmt.Errorf("request reject: %v", err)
		}

		common.SysLog(fmt.Sprintf("downloading from origin: %s, reason: %s", common.MaskSensitiveInfo(originUrl), strings.Join(reason, ", ")))
		return GetMandatorySSRFProtectedHTTPClient().Get(originUrl)
	}
}
