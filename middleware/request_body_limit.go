package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const DefaultAPIRequestBodyMaxBytes int64 = 1 << 20 // 1 MiB

func isExemptFromDefaultAPIBodyLimit(path string) bool {
	if path == "/api/plugin/task" || path == "/api/adty/site/logo" {
		return true
	}
	if strings.HasPrefix(path, "/api/adty/tickets/") && strings.HasSuffix(path, "/images") {
		return true
	}
	if strings.Contains(path, "/withdrawals/") && (strings.HasSuffix(path, "/pay") || strings.HasSuffix(path, "/paying")) {
		return true
	}
	return false
}

// DefaultAPIRequestBodyLimit enforces a default upper bound on /api/* request
// bodies while letting dedicated upload endpoints enforce their own limits (R8-19).
func DefaultAPIRequestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBytes <= 0 || c.Request.Body == nil || isExemptFromDefaultAPIBodyLimit(c.Request.URL.Path) {
			c.Next()
			return
		}
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

func AnonymousRequestBodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		maxBytes := common.GetAnonymousRequestBodyLimitBytes()
		if maxBytes <= 0 || c.Request.Body == nil {
			c.Next()
			return
		}

		originalBody := c.Request.Body
		limitedBody, err := readAnonymousRequestBody(originalBody, maxBytes)
		_ = originalBody.Close()
		if err != nil {
			if common.IsRequestBodyTooLargeError(err) {
				c.AbortWithStatus(http.StatusRequestEntityTooLarge)
				return
			}
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(limitedBody))
		c.Request.ContentLength = int64(len(limitedBody))
		c.Next()
	}
}

func readAnonymousRequestBody(body io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, common.ErrRequestBodyTooLarge
	}
	return data, nil
}
