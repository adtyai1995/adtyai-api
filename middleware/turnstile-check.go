package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

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
			// adty 任务 11.8：分站优先用自己 widget 的 secret；所有站点统一校验 hostname 与 token 长度上限。
			secret := turnstileSecret
			if site := adtySiteFromContext(c); site != nil {
				if siteSecret := AdtyTurnstileSecretFor(site, c.Request.Host); siteSecret != "" {
					secret = siteSecret
				}
			}
			adtyTurnstileCheckSite(c, secret, response)
			return
		}
		c.Next()
	}
}
