package cache

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"pansou/plugin"
)

// 预计算的哈希值映射
var (
	pluginHashCache sync.Map // 存储插件列表哈希

	// 预先计算的常用列表哈希值
	precomputedHashes sync.Map

	// 所有插件名称的哈希值
	allPluginsHash string
)

// 初始化预计算的哈希值
func init() {
	// 预计算所有插件的哈希值
	allPlugins := plugin.GetRegisteredPlugins()
	allPluginNames := make([]string, 0, len(allPlugins))
	for _, p := range allPlugins {
		allPluginNames = append(allPluginNames, p.Name())
	}
	sort.Strings(allPluginNames)
	allPluginsHash = calculateListHash(allPluginNames)
	precomputedHashes.Store("all_plugins", allPluginsHash)
}

// GeneratePluginCacheKey 为插件搜索生成缓存键
func GeneratePluginCacheKey(keyword string, plugins []string) string {
	normalizedKeyword := strings.ToLower(strings.TrimSpace(keyword))
	pluginsHash := getPluginsHash(plugins)
	keyStr := fmt.Sprintf("plugin:%s:%s", normalizedKeyword, pluginsHash)
	hash := md5.Sum([]byte(keyStr))
	return hex.EncodeToString(hash[:])
}

// GenerateCacheKey 为插件-only 搜索生成缓存键（channels / sourceType 已忽略）
func GenerateCacheKey(keyword string, channels []string, sourceType string, plugins []string) string {
	_ = channels
	_ = sourceType
	return GeneratePluginCacheKey(keyword, plugins)
}

// 获取或计算插件哈希
func getPluginsHash(plugins []string) string {
	if plugins == nil || len(plugins) == 0 {
		if hash, ok := precomputedHashes.Load("all_plugins"); ok {
			return hash.(string)
		}
		return allPluginsHash
	}

	hasNonEmptyPlugin := false
	for _, p := range plugins {
		if p != "" {
			hasNonEmptyPlugin = true
			break
		}
	}

	if !hasNonEmptyPlugin {
		if hash, ok := precomputedHashes.Load("all_plugins"); ok {
			return hash.(string)
		}
		return allPluginsHash
	}

	if len(plugins) < 5 {
		pluginsCopy := make([]string, 0, len(plugins))
		for _, p := range plugins {
			if p != "" {
				pluginsCopy = append(pluginsCopy, p)
			}
		}
		sort.Strings(pluginsCopy)
		return strings.Join(pluginsCopy, ",")
	}

	pluginsCopy := make([]string, 0, len(plugins))
	for _, p := range plugins {
		if p != "" {
			pluginsCopy = append(pluginsCopy, p)
		}
	}
	sort.Strings(pluginsCopy)
	key := strings.Join(pluginsCopy, ",")

	if hash, ok := pluginHashCache.Load(key); ok {
		return hash.(string)
	}

	hash := calculateListHash(pluginsCopy)
	pluginHashCache.Store(key, hash)
	return hash
}

// 计算列表的哈希值
func calculateListHash(items []string) string {
	h := md5.New()
	for _, item := range items {
		h.Write([]byte(item))
	}
	return hex.EncodeToString(h.Sum(nil))
}
