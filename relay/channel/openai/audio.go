package openai

import (
	"bytes"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func OpenaiTTSHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(errors.New("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)
	helper.StopStreamKeepAlive(c)

	writeUpstreamHeaders := func() {
		if c == nil || c.Writer == nil || c.Writer.Written() {
			return
		}
		for k, v := range resp.Header {
			if len(v) == 0 || !service.ShouldCopyUpstreamHeader(c, k, v) {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
		c.Writer.WriteHeader(resp.StatusCode)
	}

	usage := &dto.Usage{}

	if info.IsStream {
		var streamedAudioBytes int
		var lastParseErr error
		headersWritten := false
		helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
			if service.SundaySearch(data, "audio") {
				var chunk struct {
					Type  string `json:"type"`
					Audio string `json:"audio"`
				}
				if err := common.Unmarshal([]byte(data), &chunk); err == nil && len(chunk.Audio) > 0 {
					streamedAudioBytes += (len(chunk.Audio) * 3) / 4
				}
			}
			if service.SundaySearch(data, "usage") {
				var simpleResponse dto.SimpleResponse
				if err := common.Unmarshal([]byte(data), &simpleResponse); err != nil {
					logger.LogError(c, err.Error())
					lastParseErr = err
					sr.Error(err)
				} else if simpleResponse.Usage.TotalTokens != 0 || simpleResponse.Usage.InputTokens != 0 || simpleResponse.Usage.OutputTokens != 0 || simpleResponse.Usage.PromptTokens != 0 || simpleResponse.Usage.CompletionTokens != 0 {
					inTokens := simpleResponse.Usage.InputTokens
					if inTokens == 0 {
						inTokens = simpleResponse.Usage.PromptTokens
					}
					if inTokens > 0 {
						usage.PromptTokens = inTokens
					}
					outTokens := simpleResponse.Usage.OutputTokens
					if outTokens == 0 {
						outTokens = simpleResponse.Usage.CompletionTokens
					}
					if outTokens > 0 {
						usage.CompletionTokens = outTokens
					}
					usage.TotalTokens = max(simpleResponse.Usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
					usage.PromptTokensDetails.TextTokens = usage.PromptTokens
					audioOut := simpleResponse.Usage.CompletionTokenDetails.AudioTokens
					if audioOut <= 0 {
						audioOut = usage.CompletionTokens
					}
					usage.CompletionTokenDetails.AudioTokens = audioOut
				}
			}
			if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
				if info != nil && info.StreamStatus != nil {
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
				}
				sr.Stop(c.Request.Context().Err())
				return
			}
			if !headersWritten {
				writeUpstreamHeaders()
				headersWritten = true
			}
			if err := helper.StringData(c, data); err != nil {
				if info != nil && info.StreamStatus != nil {
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
				}
				sr.Stop(err)
				return
			}
			if info != nil {
				info.SendResponseCount++
			}
		})
		if !helper.HasWrittenStreamPayload(c) && (info == nil || info.SendResponseCount == 0) {
			if helper.ClientRequestCanceled(c) || (info != nil && info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone) {
				var cancelErr error
				if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
					cancelErr = info.StreamStatus.EndError
				} else if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
					cancelErr = c.Request.Context().Err()
				} else {
					cancelErr = errors.New("client closed request")
				}
				return nil, types.NewError(cancelErr, types.ErrorCodeRequestCancelled, types.ErrOptionWithStatusCode(499), types.ErrOptionWithSkipRetry())
			}
			if info != nil && info.StreamStatus != nil && info.StreamStatus.EndError != nil {
				return nil, types.NewOpenAIError(info.StreamStatus.EndError, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
			}
			if lastParseErr != nil {
				return nil, types.NewOpenAIError(lastParseErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			return nil, types.NewOpenAIError(errors.New("empty upstream stream response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
		}
		if usage.PromptTokens == 0 {
			usage.PromptTokens = info.GetEstimatePromptTokens()
		}
		usage.TotalTokens = max(usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
		usage.PromptTokensDetails.TextTokens = usage.PromptTokens
		if usage.CompletionTokenDetails.AudioTokens == 0 {
			if usage.CompletionTokens > 0 {
				usage.CompletionTokenDetails.AudioTokens = usage.CompletionTokens
			} else if streamedAudioBytes > 0 {
				estimatedTokens, clamp := estimatedAudioTokensFromSizeKBChecked(float64(streamedAudioBytes) / 1000.0)
				if clamp != nil && info.QuotaClamp == nil {
					info.QuotaClamp = clamp
				}
				usage.CompletionTokens = estimatedTokens
				usage.CompletionTokenDetails.AudioTokens = estimatedTokens
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
		}
	} else {
		common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
		// 读取响应体到缓冲区（受统一上限与断连保护）
		bodyBytes, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
		if apiErr != nil {
			logger.LogError(c, common.LogText("failed to read TTS response body: %v", apiErr.Error()))
			return nil, apiErr
		}
		if len(bodyBytes) == 0 {
			return nil, types.NewOpenAIError(errors.New("empty upstream TTS response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError)
		}

		// 仅在首块有效数据到达后再写入响应头与状态码
		writeUpstreamHeaders()
		c.Writer.WriteHeaderNow()
		_, err := c.Writer.Write(bodyBytes)
		if err != nil {
			logger.LogError(c, common.LogText("failed to write TTS response: %v", err))
		} else if info != nil {
			info.SendResponseCount++
		}

		usage.PromptTokens = info.GetEstimatePromptTokens()
		usage.TotalTokens = info.GetEstimatePromptTokens()

		// 计算音频时长并更新 usage
		audioFormat := "mp3" // 默认格式
		if audioReq, ok := info.Request.(*dto.AudioRequest); ok && audioReq.ResponseFormat != "" {
			audioFormat = audioReq.ResponseFormat
		}

		var duration float64
		var durationErr error

		if audioFormat == "pcm" {
			// PCM 格式没有文件头，根据 OpenAI TTS 的 PCM 参数计算时长
			// 采样率: 24000 Hz, 位深度: 16-bit (2 bytes), 声道数: 1
			const sampleRate = 24000
			const bytesPerSample = 2
			const channels = 1
			duration = float64(len(bodyBytes)) / float64(sampleRate*bytesPerSample*channels)
		} else {
			ext := "." + audioFormat
			reader := bytes.NewReader(bodyBytes)
			duration, durationErr = common.GetAudioDuration(c.Request.Context(), reader, ext)
		}

		usage.PromptTokensDetails.TextTokens = usage.PromptTokens

		if durationErr != nil {
			logger.LogWarn(c, common.LogText("failed to get audio duration: %v", durationErr))
			// 如果无法获取时长，则设置保底的 CompletionTokens，根据body大小计算
			estimatedTokens, clamp := estimatedAudioTokensFromSizeKBChecked(float64(len(bodyBytes)) / 1000.0)
			if clamp != nil && info.QuotaClamp == nil {
				info.QuotaClamp = clamp
			}
			usage.CompletionTokens = estimatedTokens
			usage.CompletionTokenDetails.AudioTokens = estimatedTokens
		} else if duration > 0 {
			// 计算 token: ceil(duration) / 60.0 * 1000，即每分钟 1000 tokens。
			// duration 解析自上游返回的音频元数据，饱和转换防止 int 回绕。
			completionTokens, clamp := common.QuotaRoundChecked(math.Ceil(duration) / 60.0 * 1000)
			if clamp != nil && info.QuotaClamp == nil {
				info.QuotaClamp = clamp
			}
			usage.CompletionTokens = completionTokens
			usage.CompletionTokenDetails.AudioTokens = completionTokens
		}
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return usage, nil
}

// estimatedAudioTokensFromSizeKB 是拿不到音频时长时的兜底估算：粗略按每 KB 约 1 token。
//
// 必须和 `:107` 的时长口径一样走 `common.QuotaRoundChecked`（饱和 + 越界留 QuotaClamp 痕迹）：
// 裸 `int()` 转换既丢掉 int32 饱和、也不会留下任何审计痕迹，billing.md:14 明令禁止
// 「把算出来的 quota / token 数用裸 cast 转 int」。单独成函数是为了让饱和这一面可测
// ——响应体大小本身在测试里到不了 int32 上限。
func estimatedAudioTokensFromSizeKB(sizeInKB float64) int {
	tokens, _ := estimatedAudioTokensFromSizeKBChecked(sizeInKB)
	return tokens
}

func estimatedAudioTokensFromSizeKBChecked(sizeInKB float64) (int, *common.QuotaClamp) {
	return common.QuotaRoundChecked(math.Ceil(sizeInKB))
}

func OpenaiSTTHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, responseFormat string) (*types.NewAPIError, *dto.Usage) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, apiErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if apiErr != nil {
		return apiErr, nil
	}

	if len(bytes.TrimSpace(responseBody)) == 0 {
		rf := strings.ToLower(strings.TrimSpace(responseFormat))
		contentType := ""
		if resp != nil {
			contentType = strings.ToLower(resp.Header.Get("Content-Type"))
		}
		if rf == "" || rf == "json" || rf == "verbose_json" || rf == "diarized_json" || strings.Contains(contentType, "application/json") {
			return types.NewOpenAIError(errors.New("empty upstream STT response"), types.ErrorCodeEmptyResponse, http.StatusInternalServerError), nil
		}
	}

	fallbackStatus := http.StatusBadGateway
	if resp != nil && resp.StatusCode >= 400 && resp.StatusCode <= 599 {
		fallbackStatus = resp.StatusCode
	}
	if oaiErr, statusCode := ExtractEmbeddedOpenAIError(responseBody, fallbackStatus); oaiErr != nil {
		return types.WithOpenAIError(*oaiErr, statusCode), nil
	}

	var responseData struct {
		Text  string `json:"text"`
		Usage *struct {
			dto.Usage
			InputTokenDetails *struct {
				TextTokens  int `json:"text_tokens"`
				AudioTokens int `json:"audio_tokens"`
			} `json:"input_token_details"`
		} `json:"usage"`
	}
	parsedJSON := common.Unmarshal(responseBody, &responseData) == nil

	// 全部校验通过后再写出响应体并正常计费
	service.IOCopyBytesGracefully(c, resp, responseBody)

	if parsedJSON && responseData.Usage != nil {
		usage := &responseData.Usage.Usage
		clampNonNegativeUsage(usage)
		if usage.PromptTokens <= 0 && usage.InputTokens > 0 {
			usage.PromptTokens = usage.InputTokens
		}
		if usage.CompletionTokens <= 0 && usage.OutputTokens > 0 {
			usage.CompletionTokens = usage.OutputTokens
		}
		if usage.PromptTokens <= 0 && usage.CompletionTokens <= 0 && usage.TotalTokens > 0 {
			usage.PromptTokens = usage.TotalTokens
		}
		if usage.TotalTokens <= 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
			// R6-014: Map OpenAI STT singular `input_token_details` (and plural
			// `input_tokens_details`) into PromptTokensDetails so AudioRatio applies.
			if details := responseData.Usage.InputTokenDetails; details != nil {
				if usage.PromptTokensDetails.AudioTokens <= 0 && details.AudioTokens > 0 {
					usage.PromptTokensDetails.AudioTokens = details.AudioTokens
				}
				if usage.PromptTokensDetails.TextTokens <= 0 && details.TextTokens > 0 {
					usage.PromptTokensDetails.TextTokens = details.TextTokens
				}
			}
			if usage.InputTokensDetails != nil {
				if usage.PromptTokensDetails.AudioTokens <= 0 && usage.InputTokensDetails.AudioTokens > 0 {
					usage.PromptTokensDetails.AudioTokens = usage.InputTokensDetails.AudioTokens
				}
				if usage.PromptTokensDetails.TextTokens <= 0 && usage.InputTokensDetails.TextTokens > 0 {
					usage.PromptTokensDetails.TextTokens = usage.InputTokensDetails.TextTokens
				}
			}
			if usage.CompletionTokens > 0 && usage.CompletionTokenDetails.TextTokens <= 0 && usage.CompletionTokenDetails.AudioTokens <= 0 {
				usage.CompletionTokenDetails.TextTokens = usage.CompletionTokens
			}
			return nil, usage
		}
	}

	promptTokens := info.GetEstimatePromptTokens()
	transcriptText := strings.TrimSpace(responseData.Text)
	if !parsedJSON || transcriptText == "" {
		transcriptText = strings.TrimSpace(string(responseBody))
	}
	// R13-011: Never settle a non-empty STT transcription at 0 tokens when
	// upstream omits `usage` and estimate tokens are missing/zero.
	if promptTokens <= 0 && transcriptText != "" {
		promptTokens = common.QuotaRound(1.0 / 60.0 * 1000)
		if est := service.ResponseText2Usage(c, transcriptText, info.UpstreamModelName, 0); est != nil && est.CompletionTokens > promptTokens {
			promptTokens = est.CompletionTokens
		}
		if promptTokens < 1 {
			promptTokens = 1
		}
	}
	usage := &dto.Usage{
		PromptTokens: promptTokens,
		TotalTokens:  promptTokens,
	}
	// R7-013: When a model has explicit audio-token pricing (e.g. gpt-4o-transcribe)
	// and returns non-JSON (response_format=text/srt/vtt) or omits `usage`, preserve
	// AudioRatio billing and estimate completion text tokens from the transcript.
	if ratio_setting.ContainsAudioRatio(info.GetBillingModelName()) {
		usage.PromptTokensDetails.AudioTokens = promptTokens
		if transcriptText != "" {
			if est := service.ResponseText2Usage(c, transcriptText, info.UpstreamModelName, promptTokens); est != nil && est.CompletionTokens > 0 {
				usage.CompletionTokens = est.CompletionTokens
				usage.CompletionTokenDetails.TextTokens = est.CompletionTokens
				usage.TotalTokens = promptTokens + est.CompletionTokens
			}
		}
	}
	return nil, usage
}
