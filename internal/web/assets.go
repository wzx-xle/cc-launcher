package web

import (
	"embed"
	"strings"
)

// assetFS 内嵌前端资源（HTML / CSS / JS）。
//
// 零外部依赖：页面不 fetch 任何互联网资源，离线可用；无前端框架、无 CDN
// 图表库——图表是手写 SVG（web-usage-stats-ui §D12）。
//
//go:embed assets/index.html assets/app.js assets/style.css
var assetFS embed.FS

// indexHTML 返回看板页面，内容里的资源版本占位符已替换成当前版本号。
//
// 版本号进 query（?v=）→ 浏览器不会拿旧资源（design.md §D12「静态资源带
// 版本化 query」）。
func indexHTML() ([]byte, error) {
	data, err := assetFS.ReadFile("assets/index.html")
	if err != nil {
		return nil, err
	}
	return []byte(strings.ReplaceAll(string(data), "__ASSET_VERSION__", assetVersion())), nil
}
