package util

import (
	"golang.org/x/net/http/httpproxy"
	"net/http"
	"net/url"
	"os"
	"pansou/config"
	"strings"
)

// ProxyFuncForTransport applies the application's proxy to custom plugin clients.
func ProxyFuncForTransport() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		raw := strings.TrimSpace(os.Getenv("PROXY"))
		if config.AppConfig != nil {
			raw = config.AppConfig.ProxyURL
		}
		if raw == "" {
			return http.ProxyFromEnvironment(req)
		}
		raw = strings.Replace(raw, "socks5h://", "socks5://", 1)
		noProxy := os.Getenv("NO_PROXY")
		if noProxy == "" {
			noProxy = os.Getenv("no_proxy")
		}
		cfg := &httpproxy.Config{HTTPProxy: raw, HTTPSProxy: raw, NoProxy: noProxy}
		return cfg.ProxyFunc()(req.URL)
	}
}
