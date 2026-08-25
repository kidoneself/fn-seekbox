package api

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware 跨域中间件
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// LoggerMiddleware 日志中间件
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()

		c.Next()

		endTime := time.Now()
		latencyTime := endTime.Sub(startTime)
		reqMethod := c.Request.Method
		reqURI := c.Request.RequestURI

		displayURI := reqURI
		if strings.Contains(reqURI, "/api/search") && strings.Contains(reqURI, "kw=") {
			if parsedURL, err := url.Parse(reqURI); err == nil {
				if keyword := parsedURL.Query().Get("kw"); keyword != "" {
					if decodedKeyword, err := url.QueryUnescape(keyword); err == nil {
						displayURI = strings.Replace(reqURI, "kw="+keyword, "kw="+decodedKeyword, 1)
					}
				}
			}
		}

		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()

		gin.DefaultWriter.Write([]byte(
			fmt.Sprintf("| %s | %s | %s | %d | %s\n",
				clientIP, reqMethod, displayURI, statusCode, latencyTime.String())))
	}
}
