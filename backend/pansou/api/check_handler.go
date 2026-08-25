package api

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"pansou/model"
	"pansou/service"
)

var (
	checkService     *service.CheckService
	checkServiceOnce sync.Once
)

func getCheckService() *service.CheckService {
	checkServiceOnce.Do(func() {
		checkService = service.NewCheckService()
	})
	return checkService
}

func CheckHandler(c *gin.Context) {
	var req model.CheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.NewErrorResponse(400, "无效的检测请求: "+err.Error()))
		return
	}

	if len(req.Items) == 0 {
		c.JSON(http.StatusBadRequest, model.NewErrorResponse(400, "items不能为空"))
		return
	}

	proxyURL := strings.TrimSpace(req.ProxyURL)
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(req.Proxy)
	}

	response, err := getCheckService().CheckWithProxy(req.Items, proxyURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.NewErrorResponse(400, "无效的代理参数: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, response)
}
