package sessiondir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSessionFile 在 projectsDir/<sub>/<sessionID>.jsonl 写一段内容。
func writeSessionFile(t *testing.T, projectsDir, sub, sessionID, content string) {
	t.Helper()
	dir := filepath.Join(projectsDir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveCwd_Hit 断言命中 cwd。
func TestResolveCwd_Hit(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "-home-u-proj", "S1",
		`{"type":"user","message":{"role":"user","content":"hello"},"cwd":"/home/u/proj","sessionId":"S1"}`+"\n")

	r := NewResolver(dir)
	if got := r.ResolveCwd("S1"); got != "/home/u/proj" {
		t.Fatalf("got %q, want /home/u/proj", got)
	}
}

// TestResolveCwd_FirstRecordWins 断言在第一个含 cwd 的记录处停止。
//
// 第 2 条记录的 cwd 与第 1 条不同 → 必须取第 1 条。
func TestResolveCwd_FirstRecordWins(t *testing.T) {
	dir := t.TempDir()
	content := `{"type":"summary","summary":"x"}` + "\n" +
		`{"cwd":"/home/u/first"}` + "\n" +
		`{"cwd":"/home/u/second"}` + "\n"
	writeSessionFile(t, dir, "enc", "S2", content)

	r := NewResolver(dir)
	if got := r.ResolveCwd("S2"); got != "/home/u/first" {
		t.Fatalf("got %q, want /home/u/first", got)
	}
}

// TestResolveCwd_NoCwdField 断言文件里没有 cwd 字段 → 空串、不报错。
func TestResolveCwd_NoCwdField(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "enc", "S3", `{"type":"user","message":{"content":"hi"}}`+"\n")

	r := NewResolver(dir)
	if got := r.ResolveCwd("S3"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

// TestResolveCwd_MissingFile 断言文件缺失 → 空串、不报错。
func TestResolveCwd_MissingFile(t *testing.T) {
	dir := t.TempDir()
	r := NewResolver(dir)
	if got := r.ResolveCwd("nope"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

// TestResolveCwd_MissingProjectsDir 断言 projects 目录不存在 → 空串。
func TestResolveCwd_MissingProjectsDir(t *testing.T) {
	r := NewResolver(filepath.Join(t.TempDir(), "does-not-exist"))
	if got := r.ResolveCwd("S4"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

// TestResolveCwd_NoPromptRetention 断言只取 cwd：返回结果里不含 prompt 内容，
// 且 resolver 不产生任何日志输出（无 prompt 泄漏路径）。
func TestResolveCwd_NoPromptRetention(t *testing.T) {
	dir := t.TempDir()
	const secret = "SUPER-SECRET-PROMPT-CONTENT"
	writeSessionFile(t, dir, "enc", "S5",
		`{"message":{"role":"user","content":"`+secret+`"},"cwd":"/home/u/proj"}`+"\n"+
			`{"message":{"role":"user","content":"`+secret+`-2"},"cwd":"/other"}`+"\n")

	r := NewResolver(dir)
	got := r.ResolveCwd("S5")
	if got != "/home/u/proj" {
		t.Fatalf("got %q, want /home/u/proj", got)
	}
	if strings.Contains(got, secret) {
		t.Fatalf("结果里出现了 prompt 内容: %q", got)
	}
}

// TestResolveCwd_RejectsPathTraversal 断言含路径分隔符的 session id 被拒绝。
func TestResolveCwd_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "enc", "S6", `{"cwd":"/home/u/proj"}`+"\n")

	r := NewResolver(dir)
	for _, bad := range []string{"../S6", "a/S6", `a\S6`, ".", ".."} {
		if got := r.ResolveCwd(bad); got != "" {
			t.Fatalf("ResolveCwd(%q) = %q, want empty", bad, got)
		}
	}
}

// TestResolveCwd_CorruptedFile 断言损坏文件不 panic、返回空串。
func TestResolveCwd_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "enc", "S7", "not json at all\n{")

	r := NewResolver(dir)
	if got := r.ResolveCwd("S7"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}
