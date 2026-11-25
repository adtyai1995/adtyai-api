package jimeng

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type ImageResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		BinaryDataBase64 []string `json:"binary_data_base64"`
		ImageUrls        []string `json:"image_urls"`
		RephraseResult   string   `json:"rephraser_result"`
		RequestID        string   `json:"request_id"`
		// Other fields are omitted for brevity
	} `json:"data"`
	RequestID   string `json:"request_id"`
	Status      int    `json:"status"`
	TimeElapsed string `json:"time_elapsed"`
}

func responseJimeng2OpenAIImage(_ *gin.Context, response *ImageResponse, info *relaycommon.RelayInfo) (*dto.ImageResponse, int64) {
	imageResponse := dto.ImageResponse{
		Created: info.StartTime.Unix(),
	}

	var nonEmptyB64s, nonEmptyURLs int64
	for _, base64Data := range response.Data.BinaryDataBase64 {
		if strings.TrimSpace(base64Data) == "" {
			continue
		}
		nonEmptyB64s++
		imageResponse.Data = append(imageResponse.Data, dto.ImageData{
			B64Json: base64Data,
		})
	}
	for _, imageUrl := range response.Data.ImageUrls {
		if strings.TrimSpace(imageUrl) == "" {
			continue
		}
		nonEmptyURLs++
		imageResponse.Data = append(imageResponse.Data, dto.ImageData{
			Url: imageUrl,
		})
	}

	return &imageResponse, max(nonEmptyB64s, nonEmptyURLs)
}

// jimengImageHandler handles the Jimeng image generation response
func jimengImageHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	var jimengResponse ImageResponse
	responseBody, readErr := helper.ReadResponseBody(c, resp, types.ErrorCodeReadResponseBodyFailed)
	if readErr != nil {
		return nil, readErr
	}
	var err error

	err = common.Unmarshal(responseBody, &jimengResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	// Check if the response indicates an error
	if jimengResponse.Code != 10000 {
		return nil, types.WithOpenAIError(types.OpenAIError{
			Message: jimengResponse.Message,
			Type:    "jimeng_error",
			Param:   "",
			Code:    fmt.Sprintf("%d", jimengResponse.Code),
		}, resp.StatusCode)
	}

	// Convert Jimeng response to OpenAI format
	fullTextResponse, billableImages := responseJimeng2OpenAIImage(c, &jimengResponse, info)
	if info != nil && billableImages > 0 {
		info.UpdateImageCount(billableImages)
	}
	jsonResponse, err := common.Marshal(fullTextResponse)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(resp.StatusCode)
	_, err = c.Writer.Write(jsonResponse)
	if err != nil {
		common.SysLog(common.LogText("failed to write Jimeng image response after upstream generation: %v", err))
	}

	return &dto.Usage{}, nil
}
