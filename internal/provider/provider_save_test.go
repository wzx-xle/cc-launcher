package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, name := range []string{"iflytek", "my-provider", "a.b", "A_1"} {
		if !ValidName(name) {
			t.Errorf("%q 应该合法", name)
		}
	}
	for _, name := range []string{"", ".", "..", "../evil", "foo/bar", `foo\bar`, "has space", strings.Repeat("x", 65)} {
		if ValidName(name) {
			t.Errorf("%q 应该被拒绝", name)
		}
	}
}

func TestManager_RejectsTraversalNames(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if _, err := m.Get("../evil"); err == nil {
		t.Error("Get 应该拒绝穿越名字")
	}
	if err := m.Delete("../evil"); err == nil {
		t.Error("Delete 应该拒绝穿越名字")
	}
	if m.Exists("../evil") {
		t.Error("Exists 应该拒绝穿越名字")
	}
	if err := m.SaveRaw("../evil", []byte(`{"env":{}}`)); err == nil {
		t.Error("SaveRaw 应该拒绝穿越名字")
	}
	// 目录外不该出现任何文件
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.json")); err == nil {
		t.Error("目录外出现了文件")
	}
}

func TestSaveRaw_WritesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	m := NewManager(dir)
	m.SetBackupDir(backups)

	first := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"a"}}`)
	if err := m.SaveRaw("p1", first); err != nil {
		t.Fatalf("SaveRaw: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "p1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(first) {
		t.Fatalf("内容 = %q，期望 %q", data, first)
	}

	// 第二次写 → 第一次的内容进备份
	second := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"b"}}`)
	if err := m.SaveRaw("p1", second); err != nil {
		t.Fatalf("SaveRaw 第二次: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(backups, "p1.json-*.bak"))
	if len(matches) != 1 {
		t.Fatalf("备份数 = %d，期望 1", len(matches))
	}
	old, _ := os.ReadFile(matches[0])
	if string(old) != string(first) {
		t.Fatalf("备份内容 = %q，期望 %q", old, first)
	}
}

func TestSaveRaw_RejectsInvalidSchema(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.SetBackupDir(filepath.Join(dir, "backups"))

	// env 不是对象 → 已知键类型错 → 校验不过
	err := m.SaveRaw("bad", []byte(`{"env":"not-an-object"}`))
	if err == nil {
		t.Fatal("非法配置应该被拒绝")
	}
	var ve *ValidationError
	if !asValidationError(err, &ve) {
		t.Fatalf("错误类型 = %T，期望 *ValidationError", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "bad.json")); statErr == nil {
		t.Fatal("校验不过时不该落盘")
	}
}

func TestSaveRaw_RejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	if err := m.SaveRaw("bad", []byte(`{not json`)); err == nil {
		t.Fatal("非法 JSON 应该被拒绝")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "bad.json")); statErr == nil {
		t.Fatal("非法 JSON 时不该落盘")
	}
}

// asValidationError 是 errors.As 的小包装（测试里不想 import errors 也行，
// 但显式写出来更清楚）。
func asValidationError(err error, target **ValidationError) bool {
	ve, ok := err.(*ValidationError)
	if ok {
		*target = ve
	}
	return ok
}
