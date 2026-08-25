package model

// FilterConfig 过滤配置
type FilterConfig struct {
	Include []string `json:"include,omitempty"` // 包含关键词列表（OR关系）
	Exclude []string `json:"exclude,omitempty"` // 排除关键词列表（AND关系）
}

// SearchRequest 搜索请求参数
type SearchRequest struct {
	Keyword      string                 `json:"kw" binding:"required"`       // 搜索关键词
	Channels     []string               `json:"channels"`                    // 已废弃：Telegram 频道列表，插件-only 模式下忽略
	Concurrency  int                    `json:"conc"`                        // 并发搜索数量
	ForceRefresh bool                   `json:"refresh"`                     // 强制刷新，不使用缓存
	ResultType   string                 `json:"res"`                         // 结果类型：all(返回所有结果)、results(仅返回results)、merge(仅返回merged_by_type)
	SourceType   string                 `json:"src"`                         // 已固定为 plugin（仅插件）；tg/all 会被忽略
	Plugins      []string               `json:"plugins"`                     // 指定搜索的插件列表，不指定则搜索全部插件
	Ext          map[string]interface{} `json:"ext"`                         // 扩展参数，用于传递给插件的自定义参数
	CloudTypes   []string               `json:"cloud_types"`                 // 指定返回的网盘类型列表，不指定则返回所有类型
	Filter       *FilterConfig          `json:"filter,omitempty"`            // 过滤配置，用于过滤返回结果
} 