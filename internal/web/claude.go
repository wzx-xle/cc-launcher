package web

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"cc-launcher/internal/safefile"
	"cc-launcher/internal/settingsschema"
)

// claudeFileItem 是 GET /api/claude/files 的列表项。
type claudeFileItem struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time,omitempty"`
}

// claudeFileSpec 是一个可编辑文件的固定映射。
type claudeFileSpec struct {
	id   string
	path string
}

// claudeFileSpecs 返回全部可编辑文件。
//
// **只有用户级的两个文件**：settings.json 与 CLAUDE.md。
//
// 刻意**不**包括：
//   - ~/.claude.json —— Claude Code 高频写它（每次会话都在改），网页的乐观锁
//     会一直撞；它是状态不是配置；里面还有 OAuth 账号信息与主机指纹
//   - 项目级文件 —— 网页挂在 daemon 里是全局的，不知道"当前项目"是哪个；
//     而且项目文件跟着 git 走，在网页上改容易和 git 打架
func (s *Server) claudeFileSpecs() []claudeFileSpec {
	specs := make([]claudeFileSpec, 0, 2)
	if s.opts.ClaudeSettingsPath != "" {
		specs = append(specs, claudeFileSpec{id: ClaudeFileSettings, path: s.opts.ClaudeSettingsPath})
	}
	if s.opts.ClaudeMemoryPath != "" {
		specs = append(specs, claudeFileSpec{id: ClaudeFileMemory, path: s.opts.ClaudeMemoryPath})
	}
	return specs
}

// claudeFilePath 按标识解析固定路径。
func (s *Server) claudeFilePath(id string) (string, bool) {
	for _, spec := range s.claudeFileSpecs() {
		if spec.id == id {
			return spec.path, true
		}
	}
	return "", false
}

// handleAPIClaudeFiles 处理 GET /api/claude/files：列出可编辑的文件。
func (s *Server) handleAPIClaudeFiles(w http.ResponseWriter, r *http.Request) {
	specs := s.claudeFileSpecs()
	items := make([]claudeFileItem, 0, len(specs))
	for _, spec := range specs {
		item := claudeFileItem{ID: spec.id, Path: spec.path}
		if fi, err := os.Stat(spec.path); err == nil {
			item.Exists = true
			item.Size = fi.Size()
			item.ModTime = fi.ModTime().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

// handleAPIClaudeFile 处理 GET /api/claude/file/{id}：读一个文件的原文。
//
// 文件不存在时返回**空内容**而不是 404：用户可能想在网页上新建一个
// CLAUDE.md（先读空、再写）。标识不认识才是 404。
func (s *Server) handleAPIClaudeFile(w http.ResponseWriter, r *http.Request) {
	path, ok := s.claudeFilePath(r.PathValue("id"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "文件不存在")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data = nil // 文件还没有 → 空内容
	}
	etag := etagOf(data)
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, fileEnvelope{Content: string(data), ETag: etag})
}

// handleAPIClaudeFilePut 处理 PUT /api/claude/file/{id}：写一个文件。
//
// 顺序：乐观锁 → 解析请求体 →（settings.json）schema 校验 → 备份 → 原子写。
func (s *Server) handleAPIClaudeFilePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path, ok := s.claudeFilePath(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "文件不存在")
		return
	}

	cur, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		cur = nil // 文件还没有 → etag 按空内容算
	}
	if !etagMatches(r.Header.Get("If-Match"), etagOf(cur)) {
		writeJSON(w, http.StatusConflict, conflictBody{
			Error:   "文件已被其他程序修改，请重新加载后再保存",
			Content: string(cur),
			ETag:    etagOf(cur),
		})
		return
	}

	body, ok := s.decodeWriteEnvelope(w, r)
	if !ok {
		return
	}
	content := []byte(body.Content)

	// settings.json 要过 schema 校验：已知键类型写错会让 Claude Code 丢弃
	// **整个** settings 文件（连带里面的凭证一起消失）。
	if id == ClaudeFileSettings {
		var raw map[string]any
		if err := json.Unmarshal(content, &raw); err != nil {
			writeJSONError(w, http.StatusBadRequest, "不是合法 JSON: "+err.Error())
			return
		}
		if res := settingsschema.Validate(raw); !res.OK() {
			writeJSON(w, http.StatusBadRequest, validationBody{
				Error:  "配置校验失败，改动没有写入",
				Errors: res.Errors,
			})
			return
		}
	}

	if err := safefile.Write(path, content, s.opts.BackupDir, safefile.DefaultKeepBackups); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	etag := etagOf(content)
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, fileEnvelope{Content: body.Content, ETag: etag})
}
