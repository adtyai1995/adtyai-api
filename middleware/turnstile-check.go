package middleware

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var turnstileVerifyHTTPClient = service.NewVerifiedDirectNoRedirectHTTPClient(10 * time.Second)

type turnstileCheckResponse struct {
	Success bool `json:"success"`
}

func TurnstileCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		common.OptionMapRWMutex.RLock()
		turnstileEnabled := common.TurnstileCheckEnabled
		turnstileSecret := common.TurnstileSecretKey
		common.OptionMapRWMutex.RUnlock()
		if turnstileEnabled {
			response := c.Query("turnstile")
			if response == "" {
				common.ApiErrorT(c, "Turnstile token is empty")
				c.Abort()
				return
			}
			// adty 任务 11.8：分站用自己 widget 的 secret，并比对 hostname
			if site := adtySiteFromContext(c); site != nil {
				if secret := AdtyTurnstileSecretFor(site, c.Request.Host); secret != "" {
					adtyTurnstileCheckSite(c, secret, response)
					return
				}
			}
			form := url.Values{
				"secret":   {turnstileSecret},
				"response": {response},
				"remoteip": {c.ClientIP()},
			}
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, adtyTurnstileSiteverifyURL, strings.NewReader(form.Encode()))
			if err != nil {
				common.SysLog(err.Error())
				common.ApiErrorT(c, "Turnstile verification failed, please refresh and try again!")
				c.Abort()
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rawRes, err := turnstileVerifyHTTPClient.Do(req)
			if err != nil {
				common.SysLog(err.Error())
				common.ApiErrorT(c, "Turnstile verification failed, please refresh and try again!")
				c.Abort()
				return
			}
			defer rawRes.Body.Close()
			var res turnstileCheckResponse
			err = common.DecodeJson(io.LimitReader(rawRes.Body, 1<<20), &res)
			if err != nil {
				common.SysLog(err.Error())
				common.ApiErrorT(c, "Turnstile verification failed, please refresh and try again!")
				c.Abort()
				return
			}
			if !res.Success {
				common.ApiErrorT(c, "Turnstile verification failed, please refresh and try again!")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
