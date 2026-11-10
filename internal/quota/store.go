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
	// Backoffs 是各**额度池**的退避状态（key = pool 哈希）。
	//
	// **为什么按池而不是按 provider**：限流是厂商针对**那把 API key** 的，
	// 而额度也是按 key 分的。一个 provider 换了 key = 换了池 → 老退避不该
	// 拦住它（否则用户修好了 key 还要干等退避结束）。
	//
	// 同一个池被多个 provider 引用时只记一份 → 不会出现"同一个池，一半退避
	// 一半没退避"的分裂状态。
	Backoffs map[string]Backoff `json:"backoffs,omitempty"`
}

// Backoff 是一个额度池的退避状态。
type Backoff struct {
	// RetryAfter 是可以再查的时刻（Unix 秒）。
	RetryAfter int64 `json:"retry_after"`
	// Seconds 是当前退避时长（秒）——下次再被限流时翻倍的基数。
	Seconds int64 `json:"seconds"`
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

	// ↓↓↓ 以下两个字段已废弃（v0.20.2 起退避改记在 File.Backoffs 里，按额度池）↓↓↓
	//
	// 保留它们**只为读老数据文件**：v0.20.1 把退避写在条目里，读出时会惰性
	// 迁移到 File.Backoffs（见 migrateBackoffs）。新写入的数据不再带这两个
	// 字段（omitempty）。
	//
	// RetryAfter 是退避到什么时候才允许再查（Unix 秒）。
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
	data, err := readFile(s.path)
	if err != nil && os.IsNotExist(err) {
		// **Windows 上文件可能正被替换**：daemon 写 quota.json 用 tmp+rename，
		// 而 rename 在目标被占用（另一个进程正在读它）时会失败 → 走"删掉目标
		// 再改名"的兜底 → 目标**短暂不存在**。这一刻读到 ErrNotExist 的消费方
		// 会误判成"没有额度记录" → 退出码非 0 → 状态栏回落到 Claude Code 自己
		// 的 rate_limits（用户看到"原始限额内容"）。
		//
		// 那个窗口是微秒级的（remove + rename 两步）→ 短暂等一下再读就能跨过。
		time.Sleep(fileReplaceRetryDelay)
		data, err = readFile(s.path)
	}
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
	f.migrateBackoffs()
	return &f, nil
}

// migrateBackoffs 把老数据文件里写在条目中的退避状态并入 File.Backoffs。
//
// v0.20.1 把退避挂在 provider 名对应的条目上；v0.20.2 起按**额度池**记录
// （限流是针对 key 的，provider 换 key = 换池 = 不该被老退避拦住）。
//
// **惰性迁移**：读的时候并一次，写回时自然变成新格式 → 一次性自愈。
// 条目里没有 pool 字段（极老的数据文件）→ 丢弃该退避（无害：会被重新限流
// 并重新记录）。
func (f *File) migrateBackoffs() {
	if f.Backoffs == nil {
		f.Backoffs = map[string]Backoff{}
	}
	for name, e := range f.Entries {
		if e.RetryAfter <= 0 {
			continue
		}
		if e.Pool != "" {
			if b, ok := f.Backoffs[e.Pool]; !ok || b.RetryAfter < e.RetryAfter {
				f.Backoffs[e.Pool] = Backoff{RetryAfter: e.RetryAfter, Seconds: e.BackoffSeconds}
			}
		}
		// 清掉条目里的旧字段 → 写回时不再带（omitempty）。
		e.RetryAfter = 0
		e.BackoffSeconds = 0
		f.Entries[name] = e
	}
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
	if err := renameFile(tmpName, s.path); err != nil {
		// Windows：目标**正被占用**（另一个进程在读它）时 rename 会失败。
		// 占用是**瞬时**的 → 先重试，别急着删目标：删了会造成"文件短暂不存在"
		// 的窗口，读它的消费方（状态栏每 300ms 调一次 ccl quota --json）会
		// 误判成"没有额度记录" → 退出码非 0 → 回落到 Claude Code 自己的
		// rate_limits。详见 Load 里的注释。
		ok := false
		for i := 0; i < renameRetries; i++ {
			time.Sleep(fileReplaceRetryDelay)
			if err := renameFile(tmpName, s.path); err == nil {
				ok = true
				break
			}
		}
		if !ok {
			// 重试仍失败 → 目标可能被**长期**占用 → 只能删掉再改名（此时那个
			// 窗口不可避免，但 Load 侧的重试会兜住它）。
			_ = os.Remove(s.path)
			if err2 := renameFile(tmpName, s.path); err2 != nil {
				return fmt.Errorf("写额度数据文件失败: %w", err2)
			}
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

// SetBackoff 写某个**额度池**的退避状态（读-改-写）。
//
// 按池而不是按 provider 名：限流是厂商针对那把 key 的，provider 换 key =
// 换池 = 老退避不该拦住它（quota-stale-and-backoff-pool §决策 2）。
//
// 失败时用它：额度数据不动（fetched_at 变旧 → stale 自动生效），只有退避
// 状态是新的。**不能走 Update**——Update 会盖上 FetchedAt=now，那会让旧数据
// 看起来是新的。
func (s *Store) SetBackoff(pool string, retryAfter, seconds int64) error {
	f, err := s.Load()
	if err != nil {
		f = &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	}
	if f.Backoffs == nil {
		f.Backoffs = map[string]Backoff{}
	}
	f.Backoffs[pool] = Backoff{RetryAfter: retryAfter, Seconds: seconds}
	return s.Save(f)
}

// UpdatePoolEntries 把额度数据写给**所有引用该额度池的 provider**。
//
// **为什么必须一起写**：额度是按 key 分的，同一把 key 的多个 provider 引用的是
// **同一个额度池** → 它们的数据必须同源。只写被点名的那个，会让两个 provider
// 显示同一把 key 的不同时刻数据（手动刷新路径踩过：serve 那条路本来就是一起
// 写的）。
func (s *Store) UpdatePoolEntries(name, pool, source string, dims []Dimension, nextAt int64) error {
	f, err := s.Load()
	if err != nil {
		f = &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	}
	now := time.Now().Unix()
	for n, e := range f.Entries {
		if n != name && e.Pool != pool {
			continue
		}
		e.Source = source
		e.Pool = pool
		e.Dimensions = dims
		e.NextAt = nextAt
		e.FetchedAt = now
		f.Entries[n] = e
	}
	// 被点名的那个即使还没有条目，也要写进去。
	if _, ok := f.Entries[name]; !ok {
		f.Entries[name] = Entry{
			Source:     source,
			Pool:       pool,
			Dimensions: dims,
			NextAt:     nextAt,
			FetchedAt:  now,
		}
	}
	return s.Save(f)
}

// UpdatePoolEntriesFor 写给**指定的一批** provider（通常是同一个额度池的全部
// 成员），并给它们**同一个** fetched_at。
//
// **为什么与 UpdatePoolEntries 分开**：UpdatePoolEntries 的成员集合是"数据文件
// 里**已有条目**的同池 provider"——它只能更新已存在的条目。而 serve 的刷新
// 路径需要**确保池内每个 provider 都有条目**（没条目的那个也得拿到数据，
// 否则它会一直缺记录）。
//
// **为什么必须统一 fetched_at**：同一把 key 的额度是**一个事实**，同池的
// provider 必须记录**同一个时刻**的数据。逐个写（每次各自取 time.Now()）会
// 在跨秒边界时差 1 秒 —— 那就不算同源了。
//
// 一次性写还有副作用收益：N 个成员只 Load/Save 一次，而不是 N 次。
func (s *Store) UpdatePoolEntriesFor(names []string, pool, source string, dims []Dimension, nextAt int64) error {
	f, err := s.Load()
	if err != nil {
		// 文件损坏 → 从空白开始重建（不让一个坏文件永久挡住刷新）。
		f = &File{Version: quotaFileVersion, Entries: map[string]Entry{}}
	}
	if f.Entries == nil {
		f.Entries = map[string]Entry{}
	}
	now := time.Now().Unix()
	for _, n := range names {
		f.Entries[n] = Entry{
			Source:     source,
			Pool:       pool,
			Dimensions: dims,
			NextAt:     nextAt,
			FetchedAt:  now,
		}
	}
	return s.Save(f)
}

// ClearBackoff 清除某个额度池的退避状态（读-改-写）。
//
// 刷新成功时调用：退避状态不再随条目重建自动清零（它住在 File.Backoffs 里，
// 不在条目里）→ 必须显式清。
func (s *Store) ClearBackoff(pool string) error {
	f, err := s.Load()
	if err != nil {
		return err
	}
	if _, ok := f.Backoffs[pool]; !ok {
		return nil
	}
	delete(f.Backoffs, pool)
	return s.Save(f)
}

// ReduceBackoff 把某个键的退避**降一级**（时长减半，下限为初始退避时长；
// 已在下限则删除该条）。
//
// 为什么不是"成功就清零"（quota-per-vendor-rate-limit §D4）：厂商的限流常是
// **速率型**的（短时间内请求太密），一次成功不代表可以立刻恢复高频。清零让
// 退避永远爬不起来——实测 13:59 已退避 4m，14:24 又从 1m 重新开始，中间那次
// 成功把退避清零了 → 反复撞 429。降一级则让退避在持续成功时缓慢恢复
// （4m → 2m → 1m → 无），在持续失败时快速爬升。
func (s *Store) ReduceBackoff(key string) error {
	f, err := s.Load()
	if err != nil {
		return err
	}
	if f == nil || f.Backoffs == nil {
		return nil
	}
	b, ok := f.Backoffs[key]
	if !ok {
		return nil
	}
	next := b.Seconds / 2
	if next < int64(initialBackoff.Seconds()) {
		delete(f.Backoffs, key)
		return s.Save(f)
	}
	b.Seconds = next
	b.RetryAfter = time.Now().Unix() + next
	f.Backoffs[key] = b
	return s.Save(f)
}

// fileReplaceRetryDelay 是"文件正被替换"时重试的等待时长。
//
// Windows 上 daemon 写 quota.json 用 tmp+rename；rename 在目标被占用（另一个
// 进程正在读）时失败 → 兜底路径会"删掉目标再改名" → 目标**短暂不存在**。
// 那个窗口是微秒级的（remove + rename 两步）→ 等 5ms 重试就能跨过。
const fileReplaceRetryDelay = 5 * time.Millisecond

// renameRetries 是 rename 失败后的重试次数（占用是瞬时的）。
const renameRetries = 3

// renameFile 是 os.Rename 的注入点（测试用：模拟 Windows 上"目标被占用"）。
var renameFile = os.Rename

// readFile 是 os.ReadFile 的注入点（测试用：模拟"文件正被替换、短暂不存在"）。
var readFile = os.ReadFile

// BackoffFor 取某个额度池的退避状态；没有则返回零值。
func (f *File) BackoffFor(pool string) Backoff {
	if f == nil || f.Backoffs == nil {
		return Backoff{}
	}
	return f.Backoffs[pool]
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
