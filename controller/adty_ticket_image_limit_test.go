package controller

import (
	"encoding/base64"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// countingReader 记下**实际被读走的字节数**。这是这条用例的核心测量：
// 「请求体有没有在解码之前被截断」只能靠读了多少来证明。
type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += int64(n)
	return n, err
}

// Measure transport reads against the configured file limit and JSON envelope.
func TestTicketImageRouteStopsReadingAtTheLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupControllerTicketTestDB(t)
	policy := model.AdtyTicketPolicy{MaxBytes: 1024, MaxCount: 2, MaxTotal: 4096, MaxSide: 100, MaxPixels: 10000}
	require.NoError(t, service.SaveAdtyTicketImagePolicy(policy))
	body := `{"data":"` + strings.Repeat("A", 8192) + `"}`
	counter := &countingReader{reader: strings.NewReader(body)}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/adty/tickets/1/images", counter)
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	PostAdtyTicketImage(c)
	assert.LessOrEqual(t, counter.read, int64(base64.StdEncoding.EncodedLen(policy.MaxBytes)+1024+1))
	var response struct {
		Success bool
		Message string
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.NotEmpty(t, response.Message)
}

// Root can raise the limit up to the supported 16 MiB ceiling.
func TestTicketImageBodyLimitIsBounded(t *testing.T) {
	setupControllerTicketTestDB(t)
	policy := model.AdtyTicketPolicy{MaxBytes: 16 << 20, MaxCount: 2, MaxTotal: 32 << 20, MaxSide: 100, MaxPixels: 10000}
	require.NoError(t, service.SaveAdtyTicketImagePolicy(policy))
	policy.MaxBytes++
	require.ErrorIs(t, service.SaveAdtyTicketImagePolicy(policy), service.ErrAdtyTicketInvalid)
	require.Equal(t, 16<<20, service.AdtyTicketImageMaxBytes())
}
