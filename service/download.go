package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	workerUrl, _, allowHTTPImage := system_setting.GetWorkerConfig()
	if workerUrl == "" {
		return nil, fmt.Errorf("worker not enabled")
	}
	if !allowHTTPImage && !strings.HasPrefix(req.URL, "https") {
		return nil, fmt.Errorf("only support https url")
	}

	// SSRF防护：验证请求URL
	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(req.URL, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return nil, fmt.Errorf("request reject: %w", err)
	}

	if !strings.HasSuffix(workerUrl, "/") {
		workerUrl += "/"
	}

	// 序列化worker请求数据
	workerPayload, err := common.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal worker payload: %v", err)
	}

	workerTimeout := 30 * time.Second
	if common.RelayTimeout > 0 {
		workerTimeout = time.Duration(common.RelayTimeout) * time.Second
	}
	resp, err := NewVerifiedDirectNoRedirectHTTPClient(workerTimeout).Post(workerUrl, "application/json", bytes.NewBuffer(workerPayload))
	if err != nil {
		return nil, sanitizeOutboundError(err)
	}
	return resp, nil
}

func DoDownloadRequest(originUrl string, reason ...string) (resp *http.Response, err error) {
	return DoDownloadRequestWithContext(context.Background(), originUrl, reason...)
}

func DoDownloadRequestWithContext(ctx context.Context, originUrl string, reason ...string) (resp *http.Response, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if workerURL, workerKey, _ := system_setting.GetWorkerConfig(); workerURL != "" {
		common.SysLog(common.LogText("downloading file from worker: %s, reason: %s", common.MaskSensitiveInfo(originUrl), strings.Join(reason, ", ")))
		req := &WorkerRequest{
			URL: originUrl,
			Key: workerKey,
		}
		return DoWorkerRequest(req)
	}
	// SSRF防护：验证请求URL（非Worker模式）
	if err := ValidateSSRFProtectedFetchURL(originUrl); err != nil {
		return nil, fmt.Errorf("request reject: %v", err)
	}

	common.SysLog(common.LogText("downloading from origin: %s, reason: %s", common.MaskSensitiveInfo(originUrl), strings.Join(reason, ", ")))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, originUrl, nil)
	if err != nil {
		return nil, sanitizeOutboundError(err)
	}
	resp, err = GetSSRFProtectedHTTPClient().Do(httpReq)
	if err != nil {
		return nil, sanitizeOutboundError(err)
	}
	return resp, nil
}
