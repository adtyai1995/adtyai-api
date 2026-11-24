package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

var outboundURLCredPattern = regexp.MustCompile(`(?i)\b((?:https?|socks5h?|wss?)://)([^/\s@]+)@`)

func sanitizeOutboundText(s string) string {
	if s == "" {
		return ""
	}
	redacted := outboundURLCredPattern.ReplaceAllString(s, "${1}***@")
	return common.MaskSensitiveInfo(redacted)
}

func sanitizeOutboundURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		clean := *parsed
		if clean.User != nil {
			clean.User = url.User("***")
		}
		clean.RawQuery = ""
		clean.Fragment = ""
		return common.MaskSensitiveInfo(clean.String())
	}
	return sanitizeOutboundText(trimmed)
}

func sanitizeOutboundError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr != nil {
		return &url.Error{
			Op:  urlErr.Op,
			URL: sanitizeOutboundURL(urlErr.URL),
			Err: sanitizeOutboundError(urlErr.Err),
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := err.Error()
	sanitized := sanitizeOutboundText(msg)
	if sanitized != msg {
		return errors.New(sanitized)
	}
	return err
}

// SanitizeOutboundError redacts embedded credentials, query secrets, and sensitive
// tokens from outbound HTTP/proxy errors while preserving context sentinels.
func SanitizeOutboundError(err error) error {
	return sanitizeOutboundError(err)
}

// GetFileTypeFromUrl 获取文件类型，返回 mime type， 例如 image/jpeg, image/png, image/gif, image/bmp, image/tiff, application/pdf
// 如果获取失败，返回 application/octet-stream
func GetFileTypeFromUrl(c *gin.Context, rawURL string, reason ...string) (string, error) {
	maskedURL := sanitizeOutboundURL(rawURL)
	response, err := DoDownloadRequest(rawURL, []string{"get_mime_type", strings.Join(reason, ", ")}...)
	if err != nil {
		sanitizedErr := sanitizeOutboundError(err)
		common.SysLog(common.LogText("fail to get file type from url: %s, error: %s", maskedURL, sanitizedErr.Error()))
		return "", sanitizedErr
	}
	defer response.Body.Close()

	if response.StatusCode != 200 {
		logger.LogError(c, common.LogText("failed to download file from %s, status code: %d", maskedURL, response.StatusCode))
		return "", fmt.Errorf("failed to download file, status code: %d", response.StatusCode)
	}

	if headerType := strings.TrimSpace(response.Header.Get("Content-Type")); headerType != "" {
		if i := strings.Index(headerType, ";"); i != -1 {
			headerType = headerType[:i]
		}
		if headerType != "application/octet-stream" {
			return headerType, nil
		}
	}

	if cd := response.Header.Get("Content-Disposition"); cd != "" {
		parts := strings.SplitSeq(cd, ";")
		for part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(strings.ToLower(part), "filename=") {
				name := strings.TrimSpace(strings.TrimPrefix(part, "filename="))
				if len(name) > 2 && name[0] == '"' && name[len(name)-1] == '"' {
					name = name[1 : len(name)-1]
				}
				if dot := strings.LastIndex(name, "."); dot != -1 && dot+1 < len(name) {
					ext := strings.ToLower(name[dot+1:])
					if ext != "" {
						mt := GetMimeTypeByExtension(ext)
						if mt != "application/octet-stream" {
							return mt, nil
						}
					}
				}
				break
			}
		}
	}

	cleanedURL := rawURL
	if q := strings.Index(cleanedURL, "?"); q != -1 {
		cleanedURL = cleanedURL[:q]
	}
	if slash := strings.LastIndex(cleanedURL, "/"); slash != -1 && slash+1 < len(cleanedURL) {
		last := cleanedURL[slash+1:]
		if dot := strings.LastIndex(last, "."); dot != -1 && dot+1 < len(last) {
			ext := strings.ToLower(last[dot+1:])
			if ext != "" {
				mt := GetMimeTypeByExtension(ext)
				if mt != "application/octet-stream" {
					return mt, nil
				}
			}
		}
	}

	var readData []byte
	limits := []int{512, 8 * 1024, 24 * 1024, 64 * 1024}
	for _, limit := range limits {
		logger.LogDebug(c, "Trying to read %d bytes to determine file type", limit)
		if len(readData) < limit {
			need := limit - len(readData)
			tmp := make([]byte, need)
			n, _ := io.ReadFull(response.Body, tmp)
			if n > 0 {
				readData = append(readData, tmp[:n]...)
			}
		}

		if len(readData) == 0 {
			continue
		}

		sniffed := http.DetectContentType(readData)
		if sniffed != "" && sniffed != "application/octet-stream" {
			return sniffed, nil
		}

		// Try HEIF/HEIC detection (Go standard library doesn't recognize it)
		if heifMime := detectHEIF(readData); heifMime != "" {
			return heifMime, nil
		}

		if _, format, err := image.DecodeConfig(bytes.NewReader(readData)); err == nil {
			switch strings.ToLower(format) {
			case "jpeg", "jpg":
				return "image/jpeg", nil
			case "png":
				return "image/png", nil
			case "gif":
				return "image/gif", nil
			case "bmp":
				return "image/bmp", nil
			case "tiff":
				return "image/tiff", nil
			default:
				if format != "" {
					return "image/" + strings.ToLower(format), nil
				}
			}
		}
	}

	// Fallback
	return "application/octet-stream", nil
}

// GetFileBase64FromUrl 从 URL 获取文件的 base64 编码数据
// Deprecated: 请使用 GetBase64Data 配合 types.NewURLFileSource 替代
// 此函数保留用于向后兼容，内部已重构为调用统一的文件服务
func GetFileBase64FromUrl(c *gin.Context, url string, reason ...string) (*types.LocalFileData, error) {
	source := types.NewURLFileSource(url)
	cachedData, err := LoadFileSource(c, source, reason...)
	if err != nil {
		return nil, err
	}

	// 转换为旧的 LocalFileData 格式以保持兼容
	base64Data, err := cachedData.GetBase64Data()
	if err != nil {
		return nil, err
	}
	return &types.LocalFileData{
		Base64Data: base64Data,
		MimeType:   cachedData.MimeType,
		Size:       cachedData.Size,
		Url:        url,
	}, nil
}

func GetMimeTypeByExtension(ext string) string {
	// Convert to lowercase for case-insensitive comparison
	ext = strings.ToLower(ext)
	switch ext {
	// Text files
	case "txt", "md", "markdown", "csv", "json", "xml", "html", "htm":
		return "text/plain"

	// Image files
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "jfif":
		return "image/jpeg"
	case "heic":
		return "image/heic"
	case "heif":
		return "image/heif"

	// Audio files
	case "mp3":
		return "audio/mp3"
	case "wav":
		return "audio/wav"
	case "mpeg":
		return "audio/mpeg"

	// Video files
	case "mp4":
		return "video/mp4"
	case "wmv":
		return "video/wmv"
	case "flv":
		return "video/flv"
	case "mov":
		return "video/mov"
	case "mpg":
		return "video/mpg"
	case "avi":
		return "video/avi"
	case "mpegps":
		return "video/mpegps"

	// Document files
	case "pdf":
		return "application/pdf"

	default:
		return "application/octet-stream" // Default for unknown types
	}
}
