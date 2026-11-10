package stats

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cc-launcher/internal/telemetry/labelkeys"
)

// TestStats_LabelsKeysAreWhitelisted 断言 stats 查询层用到的每个 metrics.labels
// 键都在落库白名单里。
//
// 为什么这条断言重要：labels 在**落库时**被裁成白名单（telemetry-db-compaction
// §D1）。若 stats 将来加了 JSON_EXTRACT(m.labels,'$.xxx') 而白名单没同步，
// 那个维度会**静默变成空**——没有报错、没有测试失败，看板上多一个全 0 的维度。
// 这条断言是白名单方案唯一的编译期保障。
//
// 实现方式：扫 internal/stats 下的 .go 源码，抠出所有 JSON_EXTRACT(...,'$.key')
// 与 labels["key"] 形态的键，逐个对照白名单。
func TestStats_LabelsKeysAreWhitelisted(t *testing.T) {
	// 匹配 JSON_EXTRACT(m.labels, '$.xxx') 里的 key
	reJSONExtract := regexp.MustCompile(`JSON_EXTRACT\(\s*\w+\.labels\s*,\s*'\$\.([^']+)'`)
	// 匹配 labels["xxx"] 形态的 Go map 取值
	reMapIndex := regexp.MustCompile(`(?i)labels\["([^"]+)"\]`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// 扫全部 .go（含测试文件——测试里也可能写新的 key 断言）
	all, _ := filepath.Glob("*.go")
	files = all

	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") && strings.Contains(f, "labels_whitelist_test") {
			continue // 本文件自身（正则字面量会自我匹配）
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, m := range reJSONExtract.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
		}
		for _, m := range reMapIndex.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
		}
	}

	if len(seen) == 0 {
		t.Fatal("一条 labels 键都没扫到——扫描正则失效了，这条断言会变成空转")
	}
	for k := range seen {
		if !labelkeys.Contains(k) {
			t.Errorf("stats 读了 labels 的 %q，但它不在落库白名单里 → 该维度会静默变空。\n"+
				"白名单: %v\n加进 internal/telemetry/labelkeys.Stored 或改掉 stats 的查询。", k, labelkeys.Stored)
		}
	}
}
