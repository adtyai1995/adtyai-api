package router

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, assets WebAssets) {
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetTaskPluginProtocolRouter(router)
	SetVideoRouter(router)
	SetTaskRouter(router)
	SetAdtyWebRoutes(router) // 二开：支付跳回和按站点的 robots.txt（任务 11.3）
	pluginDispatcher := SetPluginRouter(router)
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog(common.LogText("FRONTEND_BASE_URL is ignored on master node"))
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, assets, pluginDispatcher)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		setWebRouter(router, assets, pluginDispatcher, func(c *gin.Context) {
			if service.AdtySiteForRequest(c) == nil {
				c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
				c.Abort()
			}
		})
	}
}
