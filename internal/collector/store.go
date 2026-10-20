// Package collector 实现 OTel HTTP 接收 + 本地 SQLite 持久化。
//
// 设计要点（见 design.md）：
//   - OTel only metrics：logs / traces 由 forwarder 处理，本地永不落盘
//   - SQLite WAL：单写多读，ccl stats 与 daemon 并发安全
//   - 异步写入队列：HTTP handler 不阻塞于慢盘；10k 上限触发背压（丢最旧）
package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Session 是单个 Claude Code 会话的元数据。
// 一行写入 sessions 表；metrics 通过 session_id 外键到该行。
type Session struct {
	ID        string
	StartedAt time.Time
	EndedAt   *time.Time // 可空
	Provider  string
	Cwd       string
	Model     string
}

// Metric 是 OTel 数据点的领域对象。
// 一个 NumberDataPoint 对应一行 metrics 表。
type Metric struct {
	SessionID   string
	Name        string
	Value       float64
	Labels      map[string]string
	CapturedAt  time.Time
}

// Store 是 SQLite 写入端。daemon 持有，ccl stats 不会复用（ccl stats 直接打开 db）。
type Store struct {
	db   *sql.DB
	path string

	queue   chan metricWrite
	dropped atomic.Uint64 // 累计因背压丢弃的条目数

	closed   chan struct{}
	closeOnce sync.Once
	wg       sync.WaitGroup
}

// metricWrite 是写入队列里的载荷。
type metricWrite struct {
	kind string // "session" or "metric"
	data []byte // JSON-encoded Session or Metric
}

// writeQueueSize 是写入队列容量。超过则丢最旧。
const writeQueueSize = 10000

// openStoreOptions 控制 Open 的可选行为。
type openStoreOptions struct {
	now func() time.Time
}

// OpenStoreOption 配置 OpenStore 的可选参数。
type OpenStoreOption func(*openStoreOptions)

// WithClock 注入时钟（测试用）。
func WithClock(now func() time.Time) OpenStoreOption {
	return func(o *openStoreOptions) { o.now = now }
}

// OpenStore 打开（或创建）SQLite 文件，启用 WAL，启动 worker。
//
// 幂等：
//   - 文件不存在 → 自动创建 + 建表
//   - 文件已存在   → 直接打开（schema 已有）
//
// caller 持有返回的 *Store，结束时调 Close()。
func OpenStore(path string, opts ...OpenStoreOption) (*Store, error) {
	o := openStoreOptions{now: time.Now}
	for _, opt := range opts {
		opt(&o)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("建 db 目录失败: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// 单连接即可——WAL 模式下读不阻塞单写。
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("启用 WAL 失败: %w", err)
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("设置 synchronous 失败: %w", err)
	}

	if err := createSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := migrateStore(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{
		db:     db,
		path:   path,
		queue:  make(chan metricWrite, writeQueueSize),
		closed: make(chan struct{}),
	}

	s.wg.Add(1)
	go s.worker()

	return s, nil
}

// Path 返回 db 文件路径（测试 / debug 用）。
func (s *Store) Path() string { return s.path }

// createSchema 幂等建表 + 索引。schema 是稳定契约：列变更需要迁移。
func createSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			started_at INTEGER NOT NULL,
			ended_at INTEGER,
			provider TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			name TEXT NOT NULL,
			value REAL NOT NULL,
			labels TEXT NOT NULL DEFAULT '{}',
			captured_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_session_time
			ON metrics(session_id, captured_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_cwd_time
			ON sessions(cwd, started_at)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("建表失败 (%s): %w", q, err)
		}
	}
	return nil
}

// schemaVersion 是 db 的迁移版本号（存 SQLite PRAGMA user_version）。
//
//	1 = 回填历史 sessions.started_at（design Decision 3）
const schemaVersion = 1

// migrateStore 按 PRAGMA user_version 驱动一次性数据迁移。
//
// 幂等：user_version >= schemaVersion 直接跳过；回填语句本身也可重复执行。
// 新建 db（无任何 session 行）同样走一遍回填——空表上是无害的 no-op。
func migrateStore(db *sql.DB) error {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("读 user_version 失败: %w", err)
	}
	if v >= schemaVersion {
		return nil
	}
	if err := backfillSessionStartTimes(db); err != nil {
		return fmt.Errorf("回填 sessions.started_at 失败: %w", err)
	}
	// PRAGMA 不支持参数占位符；schemaVersion 是包内常量 int，非外部输入。
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("写 user_version 失败: %w", err)
	}
	return nil
}

// backfillSessionStartTimes 修正被污染的历史 sessions.started_at。
//
// 旧版本把 started_at 写成"最后一次 OTLP export 到达 daemon 的时刻"，且每次
// export 都覆盖它。这里用 metrics 表里一直是准的 captured_at 还原真实起始：
//
//	claude_code.session.count 的 MIN(captured_at)   ← session 创建时刻的权威观测
//	回退:该 session 全部 metric 的 MIN(captured_at)   ← 最早的活动时刻
//
// 没有任何 metric 的 session 行：COALESCE 两项都是 NULL，WHERE 条件不匹配
// → 原样保留（不写 NULL、不删行）。
func backfillSessionStartTimes(db *sql.DB) error {
	const q = `
UPDATE sessions SET started_at = COALESCE(
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id AND m.name = 'claude_code.session.count'),
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id)
)
WHERE started_at <> COALESCE(
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id AND m.name = 'claude_code.session.count'),
    (SELECT MIN(m.captured_at) FROM metrics m
      WHERE m.session_id = sessions.id)
)`
	_, err := db.Exec(q)
	return err
}

// WriteSession 把 Session 入队（非阻塞；超 10k 丢最旧）。
//
// session 由 caller 保证 ID 非空。重复 ID 走 ON CONFLICT DO UPDATE 语义：
// started_at 取 MIN(现有, 新值)（只降不升），其余列被新值覆盖。
func (s *Store) WriteSession(sess Session) error {
	if sess.ID == "" {
		return errors.New("session.id 不能为空")
	}
	if sess.StartedAt.IsZero() {
		sess.StartedAt = time.Now()
	}
	return s.enqueue(metricWrite{kind: "session", data: mustJSON(sess)})
}

// WriteMetric 把 Metric 入队（非阻塞；超 10k 丢最旧）。
//
// SessionID 可为空——孤立 metric 仍可聚合。
func (s *Store) WriteMetric(m Metric) error {
	if m.Name == "" {
		return errors.New("metric.name 不能为空")
	}
	if m.CapturedAt.IsZero() {
		m.CapturedAt = time.Now()
	}
	if m.Labels == nil {
		m.Labels = map[string]string{}
	}
	return s.enqueue(metricWrite{kind: "metric", data: mustJSON(m)})
}

// Dropped 返回累计因背压丢弃的条目数。
func (s *Store) Dropped() uint64 { return s.dropped.Load() }

// enqueue 把载荷塞进队列；若已满，丢最旧再入队。
func (s *Store) enqueue(w metricWrite) error {
	select {
	case <-s.closed:
		return errors.New("store 已关闭")
	default:
	}
	// 非阻塞入队：满则丢最旧。
	for {
		select {
		case s.queue <- w:
			return nil
		default:
			// 队列满：尝试丢一个最旧的。
			select {
			case <-s.queue:
				s.dropped.Add(1)
			default:
				// 极端竞争：另一个 worker 已 drain，直接入队。
				s.queue <- w
				return nil
			}
		}
	}
}

// worker 单线程消费队列，写 SQLite。
func (s *Store) worker() {
	defer s.wg.Done()
	for w := range s.queue {
		if err := s.writeOne(w); err != nil {
			log.Printf("collector: store 写入失败: %v", err)
		}
	}
}

func (s *Store) writeOne(w metricWrite) error {
	switch w.kind {
	case "session":
		var sess Session
		if err := json.Unmarshal(w.data, &sess); err != nil {
			return err
		}
		return s.insertSession(sess)
	case "metric":
		var m Metric
		if err := json.Unmarshal(w.data, &m); err != nil {
			return err
		}
		return s.insertMetric(m)
	default:
		return fmt.Errorf("未知 kind %q", w.kind)
	}
}

func (s *Store) insertSession(sess Session) error {
	var endedAt *int64
	if sess.EndedAt != nil {
		t := sess.EndedAt.UnixMilli()
		endedAt = &t
	}
	// ON CONFLICT DO UPDATE（而非 INSERT OR REPLACE）：
	//   - started_at 只降不升（MIN(现有, 新值)）——后续 export batch 的
	//     captured_at 更大，绝不能把 session 起始时刻往前推。
	//   - 其余列（ended_at / provider / cwd / model）保持"后续 batch 覆盖"
	//     语义，与原 INSERT OR REPLACE 一致。
	_, err := s.db.Exec(
		`INSERT INTO sessions
			(id, started_at, ended_at, provider, cwd, model)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			started_at = MIN(sessions.started_at, excluded.started_at),
			ended_at   = excluded.ended_at,
			provider   = excluded.provider,
			cwd        = excluded.cwd,
			model      = excluded.model`,
		sess.ID,
		sess.StartedAt.UnixMilli(),
		endedAt,
		sess.Provider,
		sess.Cwd,
		sess.Model,
	)
	return err
}

func (s *Store) insertMetric(m Metric) error {
	labelsJSON, err := json.Marshal(m.Labels)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO metrics
			(session_id, name, value, labels, captured_at)
			VALUES (?, ?, ?, ?, ?)`,
		m.SessionID,
		m.Name,
		m.Value,
		string(labelsJSON),
		m.CapturedAt.UnixMilli(),
	)
	return err
}

// Close 停止接收，等队列清空，做 WAL checkpoint，关闭 db。
//
// 超时由 ctx 控制；返回 context.DeadlineExceeded 时调用方应升级到 SIGKILL。
// 关后 enqueue 永远返回 error。
func (s *Store) Close(ctx context.Context) error {
	var closeErr error
	s.closeOnce.Do(func() {
		close(s.closed)
		close(s.queue)
		// 等 worker 把队列里剩余条目写完。
		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			closeErr = ctx.Err()
		}

		// WAL checkpoint（即使超时也尽量做一次；不强制）。
		if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			log.Printf("collector: WAL checkpoint 失败: %v", err)
		}
		if err := s.db.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Session / Metric 都是内部结构，可控；不该走到这里。
		panic(fmt.Sprintf("mustJSON: %v", err))
	}
	return b
}