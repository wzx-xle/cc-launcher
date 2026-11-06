package quota

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestStoreAtomicWrite 断言**原子写**：并发读永远读不到半个文件。
//
// 读者要么看到旧内容、要么看到新内容——不能看到截断的 JSON。
func TestStoreAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)

	// 先写一份初始内容
	if err := st.Update("p1", "minimax", "pool-1", []Dimension{{ID: "interval", Kind: KindWindow, Unit: "%"}}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, 16)

	// 读者：持续读，断言每次都是完整 JSON
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(st.Path())
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					errCh <- err
					return
				}
				var f File
				if err := json.Unmarshal(data, &f); err != nil {
					errCh <- err
					return
				}
				if f.Version != quotaFileVersion {
					errCh <- os.ErrInvalid
					return
				}
			}
		}()
	}

	// 写者：反复写不同内容
	for i := 0; i < 50; i++ {
		dims := make([]Dimension, 0, 50)
		for j := 0; j < 50; j++ {
			dims = append(dims, Dimension{ID: "interval", Kind: KindWindow, Unit: "%", Used: f64(float64(j))})
		}
		if err := st.Update("p1", "minimax", "pool-1", dims); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("并发读看到了不完整的文件: %v", err)
	}
}

// TestStoreFilePermissions 断言数据文件权限是 0600（可能含 provider 名与用量）。
func TestStoreFilePermissions(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Update("p1", "minimax", "pool-1", nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Errorf("权限 = %o, want 0600", perm)
	}
}

// TestStoreNoPlaintextKey 断言数据文件里**没有明文 API key**（只存哈希）。
func TestStoreNoPlaintextKey(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	pool := PoolID("https://www.minimax.cn/v1/token_plan/remains", "sk-secret-value")
	if err := st.Update("minimax-m3", "minimax", pool, []Dimension{{ID: "interval", Kind: KindWindow}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret-value") {
		t.Errorf("数据文件不该含明文 key: %s", data)
	}
	if !strings.Contains(string(data), pool) {
		t.Errorf("数据文件应含 pool 哈希: %s", data)
	}
}

// TestStorePrune 断言：已不存在的 provider 的条目被清掉。
func TestStorePrune(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Update("keep", "minimax", "p1", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Update("gone", "minimax", "p2", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Prune(map[string]bool{"keep": true}); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.EntryFor("keep"); !ok {
		t.Error("keep 应还在")
	}
	if _, ok := f.EntryFor("gone"); ok {
		t.Error("gone 应被清掉")
	}
}

// TestStoreLoadMissingFile 断言：文件不存在不算错误（"还没查过额度"是正常状态）。
func TestStoreLoadMissingFile(t *testing.T) {
	st := NewStore(t.TempDir())
	f, err := st.Load()
	if err != nil {
		t.Fatalf("文件不存在不该报错: %v", err)
	}
	if len(f.Entries) != 0 {
		t.Errorf("entries 应为空: %+v", f.Entries)
	}
}

// TestStoreDimensionRoundTrip 断言维度存盘→读出→再存盘不丢信息
// （used_percentage 是派生的，往返必须保持一致）。
func TestStoreDimensionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	dims := []Dimension{
		{ID: "interval", Label: "5h", Kind: KindWindow, Duration: 18000e9, Used: f64(59), Limit: f64(100), Unit: "%"},
		{ID: "balance", Label: "CNY", Kind: KindBalance, Remaining: f64(110), Unit: "CNY"},
	}
	if err := st.Update("p1", "minimax", "pool-1", dims); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	e, ok := f.EntryFor("p1")
	if !ok {
		t.Fatal("条目应存在")
	}
	if len(e.Dimensions) != 2 {
		t.Fatalf("应有 2 个维度, got %d", len(e.Dimensions))
	}
	got := e.Dimensions[0]
	if got.Label != "5h" || got.Kind != KindWindow {
		t.Errorf("维度往返丢了字段: %+v", got)
	}
	if pct := got.UsedPct(); pct == nil || *pct != 59 {
		t.Errorf("百分比往返后应为 59, got %v", pct)
	}
	if got.Duration != 18000e9 {
		t.Errorf("时长往返后应为 18000s, got %v", got.Duration)
	}

	// 再存一次，读出来的 JSON 里百分比还在
	out, err := json.Marshal(e.Dimensions)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\"used_percentage\":59") {
		t.Errorf("JSON 应带 used_percentage: %s", out)
	}
	if !strings.Contains(string(out), "\"duration_seconds\":18000") {
		t.Errorf("JSON 应带 duration_seconds: %s", out)
	}
}

// TestStoreUpdateKeepsOtherEntries 断言 Update 是**读-改-写**（不覆盖别人的条目）。
func TestStoreUpdateKeepsOtherEntries(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Update("a", "minimax", "p1", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Update("b", "deepseek", "p2", nil); err != nil {
		t.Fatal(err)
	}
	f, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != 2 {
		t.Errorf("两个条目都该在: %+v", f.Entries)
	}
	if filepath.Base(st.Path()) != "quota.json" {
		t.Errorf("文件名 = %q", filepath.Base(st.Path()))
	}
}
