package coze

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	hostcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *common.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

// ConvertAudioRequest implements channel.Adaptor.
func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *common.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("not implemented")
}

// ConvertClaudeRequest implements channel.Adaptor.
func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *common.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("not implemented")
}

// ConvertEmbeddingRequest implements channel.Adaptor.
func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *common.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("not implemented")
}

// ConvertImageRequest implements channel.Adaptor.
func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *common.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("not implemented")
}

// ConvertOpenAIRequest implements channel.Adaptor.
func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *common.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	return convertCozeChatRequest(c, *request), nil
}

// ConvertOpenAIResponsesRequest implements channel.Adaptor.
func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *common.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("not implemented")
}

// ConvertRerankRequest implements channel.Adaptor.
func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("not implemented")
}

type cozeCancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cozeCancelBody) Close() error {
	err := b.ReadCloser.Close()
	if b.cancel != nil {
		b.cancel()
	}
	return err
}

// DoRequest implements channel.Adaptor.
func (a *Adaptor) DoRequest(c *gin.Context, info *common.RelayInfo, requestBody io.Reader) (any, error) {
	if info.IsStream {
		return channel.DoApiRequest(a, c, info, requestBody)
	}
	// R2-P1-08: 在第 1 步创建对话前先检查客户端取消并获取解耦上下文，避免第 1 步或
	// 轮询/拉取详情期间客户端断开导致第 2/3 步被中止而触发 100% 退费。
	upstreamCtx, upstreamCancel, cancelErr := channel.DetachedNonStreamContext(c, info, 0)
	if cancelErr != nil {
		return nil, cancelErr
	}
	keepCancel := false
	defer func() {
		if !keepCancel && upstreamCancel != nil {
			upstreamCancel()
		}
	}()
	// 首先发送创建消息请求，成功后再发送获取消息请求
	// 发送创建消息请求
	resp, err := channel.DoApiRequest(a, c, info, requestBody)
	if err != nil {
		return nil, err
	}
	// 解析 resp
	defer resp.Body.Close()
	var cozeResponse CozeChatResponse
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if err := hostcommon.Unmarshal(respBody, &cozeResponse); err != nil {
		return nil, fmt.Errorf("failed to parse coze chat response: %w", err)
	}
	if cozeResponse.Code != 0 {
		return nil, errors.New(cozeResponse.Msg)
	}
	c.Set("coze_conversation_id", cozeResponse.Data.ConversationId)
	c.Set("coze_chat_id", cozeResponse.Data.Id)
	// 轮询检查消息是否完成（R6-045 / R7-017 / R10-017 / R2-P1-08: 使用解耦上下文，
	// 轮询完成后释放 pollCtx，详情请求沿用 upstreamCtx 并在响应体关闭时释放 upstreamCancel）
	pollCtx, pollCancel := context.WithTimeout(upstreamCtx, 2*time.Minute)
	ticker := time.NewTicker(time.Second)
	for {
		err, isComplete := checkIfChatComplete(pollCtx, a, c, info)
		if err != nil {
			ticker.Stop()
			pollCancel()
			return nil, err
		}
		if isComplete {
			break
		}
		select {
		case <-pollCtx.Done():
			ticker.Stop()
			pollErr := pollCtx.Err()
			pollCancel()
			if errors.Is(pollErr, context.DeadlineExceeded) {
				return nil, errors.New("coze chat completion poll timed out")
			}
			return nil, pollErr
		case <-ticker.C:
		}
	}
	ticker.Stop()
	pollCancel()
	// 发送获取消息请求
	detailResp, err := getChatDetail(upstreamCtx, a, c, info)
	if err != nil {
		return nil, err
	}
	if detailResp != nil && detailResp.Body != nil && upstreamCancel != nil {
		detailResp.Body = &cozeCancelBody{ReadCloser: detailResp.Body, cancel: upstreamCancel}
		keepCancel = true
	}
	return detailResp, nil
}

// DoResponse implements channel.Adaptor.
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *common.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.IsStream {
		usage, err = cozeChatStreamHandler(c, info, resp)
	} else {
		usage, err = cozeChatHandler(c, info, resp)
	}
	return
}

// GetChannelName implements channel.Adaptor.
func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

// GetModelList implements channel.Adaptor.
func (a *Adaptor) GetModelList() []string {
	return ModelList
}

// GetRequestURL implements channel.Adaptor.
func (a *Adaptor) GetRequestURL(info *common.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/v3/chat", info.ChannelBaseUrl), nil
}

// Init implements channel.Adaptor.
func (a *Adaptor) Init(info *common.RelayInfo) {

}

// SetupRequestHeader implements channel.Adaptor.
func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *common.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}
