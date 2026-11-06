package quota

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// quotaFileVersion 是数据文件的格式版本。
const quotaFileVersion = 1

// quotaFileName 是数据文件名（放在 ~/.cc-launcher/ 下）。
const quotaFileName = "quota.json"

// File 是额度数据文件的结构。
//
// **按 provider 名索引**：读取侧（`ccl quota --provider X`）要按名字直接找。
type File struct {
	// Version 是格式版本。
	Version int `json:"version"`
	// UpdatedAt 是文件最后一次写入的时间（Unix 秒）。
	UpdatedAt int64 `json:"updated_at"`
	// Entries 是各 provider 的额度条目。
	Entries map[string]Entry `json:"entries"`
}

// Entry 是单个 provider 的额度条目。
type Entry struct {
	// Source 是产出这条数据的 adapter 名。
	Source string `json:"source"`
	// Pool 是额度池标识（sha256(端点 + "\x00" + API key)）——**只存哈希，
	// 不存明文凭证**。
	Pool string `json:"pool"`
	// FetchedAt 是这条数据的抓取时间（Unix 秒）。
	//
	// **它就是"上次刷新时间"**：serve 的周期判定必须从**文件里**读它（而不是
	// daemon 的内存 map），这样 `ccl quota --refresh` 刚写过的 provider，serve
	// 下一轮会自动跳过——两个写者天然互斥，不需要文件锁。
	FetchedAt int64 `json:"fetched_at"`
	// Dimensions 是额度维度列表。
	//
	// 冗余存储：同一个额度池被多个 provider 引用时内容重复——冗余换来读取侧
	// 零查找成本，且每个条目自带 fetched_at（新鲜度判定不用二次查询）。
	Dimensions []Dimension `json:"dimensions"`

	// NextAt 是下次该刷的时刻（Unix 秒）——**相位错开**用。
	//
	// 每个额度池在自己的刷新周期内占一个固定相位：刷新时刻 = 该相位 + k×周期。
	// 刷新后用 **NextAt 递推**（NextAt += interval）而不是"now + interval"重算
	// ——后者会把每次刷新的耗时累积进周期，且相位会漂。
	//
	// 0 = 还没分配（老数据文件 / 首次）→ 退回"fetched_at + interval"判定。
	NextAt int64 `json:"next_at,omitempty"`
	// RetryAfter 是退避到什么时候才允许再查（Unix 秒）——**429 退避**用。
	//
	// 持久化（而不是记在 daemon 内存里）：`ccl quota --refresh` 是另一个
	// 进程，它不知道要退避；serve 重启后记忆也会丢。
	RetryAfter int64 `json:"retry_after,omitempty"`
	// BackoffSeconds 是当前退避时长（秒）——下次再被限流时翻倍的基数。
	BackoffSeconds int64 `json:"backoff_seconds,omitempty"`
}

// Store 管理额度数据文件（~/.cc-launcher/quota.json）。
type Store struct {
	path string
}

// NewStore 创建 Store，baseDir 是 ~/.cc-launcher 目录。
func NewStore(baseDir string) *Store {
	return &Store{path: filepath.Join(baseDir, quotaFileName)}
}

// Path 返回数据文件路径。
func (s *Store) Path() string { return s.path }

// Load 读数据文件。
//
// 文件不存在 → 返回空 File（version 正确、entries 空），**不算错误**
// （"还没查过额度"是正常状态，不是故障）。
// JSON 损坏 → 返回 error（让调用方决定怎么处理；读取侧应当把它当成"没有
// 记录"而不是崩掉）。
func (s *Store) Load() (*File, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Version: quotaFileVersion, Entries: map[string]Entry{}}, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("额度数据文件解析失败 (%s): %w", s.path, err)
	}
	if f.Entries == nil {
		f.Entries = map[string]Entry{}
	}
	if f.Version == 0 {
		f.Version = quotaFileVersion
	}
	return &f, nil
}

// Save 原子写数据文件。
//
// **原子写**（临时文件 + rename，与 settingsfile.go 一致）→ 读取侧并发读不会
// 读到半个文件。权限 **0600**（文件里可能含 provider 名与用量信息）。
func (s *Store) Save(f *File) error {
	if f.Entries == nil {
		f.Entries = map[string]Entry{}
	}
	if f.Version == 0 {
		f.Version = quotaFileVersion
	}
	f.UpdatedAt = time.Now().Unix()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建额度数据目录失败: %w", err)
	}
	_ = os.Chmod(dir, 0700)

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化额度数据失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "quota-*.tmp")
	if err != nil {
		return fmt.Errorf("创建额度数据临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置额度数据文件权限失败: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写额度数据文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭额度数据文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		// Windows：目标已存在时 rename 失败 → 删掉目标再试一次。
		_ = os.Remove(s.path)
		if err2 := os.Rename(tmpName, s.path); err2 != nil {
			return fmt.Errorf("写额度数据文件失败: %w", err2)
		}
	}
	return nil
}

// Update 写一个 provider 的额度条目（**读-改-写**，不是全量重建）。
//
// 为什么不用全量重建：serve 与 `ccl quota --refresh` 会**并发写同一个文件**。
// 读-改-写让两边的写冲突只剩"读和写之间"的几毫秒，且冲突时写的是**同一个
// 额度池的数据**（内容等价）→ 无害；全量重建则可能整份覆盖掉对方的条目。
//
// 最坏后果是"某个条目短暂缺失一个刷新周期"，serve 下轮自愈——不需要文件锁。
func (s *Store) Update(name, source, pool string, dims []Dimension) error {
	return s.UpdateEntry(name, Entry{Source: source, Pool: pool, Dimensions: dims})
}

// UpdateEntry 写一个**完整**的条目（含相位 next_at 与退避字段）。
//
// FetchedAt 由本函数盖上（写文件的那一刻就是抓取的那一刻）；其余字段由调用
// 方填好带进来。
func (s *Store) UpdateEntry(name string, e Entry) error {
	f, err := s.Load()
	if err != nil {
		// 文件损坏 → 从空白开始重建（不让一个坏文件永久挡住刷新）。
		f = &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	}
	e.FetchedAt = time.Now().Unix()
	f.Entries[name] = e
	return s.Save(f)
}

// UpdateBackoff 只更新某个条目的退避字段（**不动**额度数据与 fetched_at）。
//
// 失败时用它：额度数据要保持旧值（fetched_at 变旧 → stale 自动生效），
// 只有退避状态是新的。**不能走 Update**——Update 会盖上 FetchedAt=now，
// 那会让旧数据看起来是新的（stale 永远不生效）。
func (s *Store) UpdateBackoff(name string, retryAfter, backoffSeconds int64) error {
	f, err := s.Load()
	if err != nil {
		f = &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	}
	e := f.Entries[name]
	e.RetryAfter = retryAfter
	e.BackoffSeconds = backoffSeconds
	f.Entries[name] = e
	return s.Save(f)
}

// Prune 删掉已不存在的 provider 的条目（读-改-写）。
//
// live 是"当前仍存在的 provider 名"集合。已删除的 provider 的残留条目会被
// 清掉——数据文件不该记着用户已经删掉的 provider 的额度。
func (s *Store) Prune(live map[string]bool) error {
	f, err := s.Load()
	if err != nil {
		return err
	}
	changed := false
	for name := range f.Entries {
		if !live[name] {
			delete(f.Entries, name)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.Save(f)
}

// EntryFor 取某个 provider 的条目；没有则返回 ("", false)。
func (f *File) EntryFor(name string) (Entry, bool) {
	if f == nil || f.Entries == nil {
		return Entry{}, false
	}
	e, ok := f.Entries[name]
	return e, ok
}
