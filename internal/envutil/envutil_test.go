package envutil

import (
	"reflect"
	"testing"
)

// TestUpsert_ReplaceExistingKey 覆盖 spec/launcher-provider-env §"upsertEnv helper"
// Scenario "existing key is replaced"。
func TestUpsert_ReplaceExistingKey(t *testing.T) {
	env := []string{"PATH=/usr/bin", "FOO=1"}
	got := Upsert(env, "PATH", "/bin")
	want := []string{"PATH=/bin", "FOO=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Upsert() = %v, want %v", got, want)
	}
}

// TestUpsert_AppendMissingKey 覆盖 spec §"missing key is appended"。
func TestUpsert_AppendMissingKey(t *testing.T) {
	env := []string{"FOO=1"}
	got := Upsert(env, "BAR", "2")
	want := []string{"FOO=1", "BAR=2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Upsert() = %v, want %v", got, want)
	}
}

// TestUpsert_NilSlice 覆盖 tasks.md §1.1 "空 slice 边界"——nil slice append 必须
// 不 panic 并返回 [key=value]。
func TestUpsert_NilSlice(t *testing.T) {
	var env []string // nil
	got := Upsert(env, "FOO", "bar")
	want := []string{"FOO=bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Upsert() = %v, want %v", got, want)
	}
}

// TestUpsert_ReplaceOnlyFirstMatch 防御性：env 里同 key 出现多次（POSIX 非法但
// 实际可能——老 ccl 老 settings.json 残留），Upsert 只替换第一次出现，保持长度不变。
func TestUpsert_ReplaceOnlyFirstMatch(t *testing.T) {
	env := []string{"FOO=first", "BAR=1", "FOO=second"}
	got := Upsert(env, "FOO", "new")
	want := []string{"FOO=new", "BAR=1", "FOO=second"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Upsert() = %v, want %v", got, want)
	}
}
