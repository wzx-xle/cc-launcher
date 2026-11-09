package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"cc-launcher/internal/provider"
	"cc-launcher/internal/templates"
)

// fileEnvelope 是"读一个可编辑文件"的统一响应体。
//
// 为什么用信封而不是直接把文件内容当响应体：
//   - provider 是 JSON、CLAUDE.md 是 Markdown —— 响应体的类型没法统一
//   - etag 必须一起给客户端（乐观锁的令牌），塞进信封最省事
//
// Content 是**原文**（逐字节），不是解析后再序列化的结果 —— 用户手写的
// 缩进、字段顺序都要原样保留。
type fileEnvelope struct {
	Content string `json:"content"`
	ETag    string `json:"etag"`
}

// writeEnvelope 是写接口的请求体。
//
// 写接口的 body 一律是 JSON 信封（哪怕写的是 Markdown 文件）：这样
// Content-Type: application/json 才是诚实的，CSRF 闸也才站得住。
type writeEnvelope struct {
	Content string `json:"content"`
}

// conflictBody 是 409（乐观锁冲突）的响应体。
//
// 把**当前文件内容**一起带回去：客户端可以据此提示"文件已被其他程序修改"，
// 也可以让用户手动合并，而不是只能干瞪眼。
type conflictBody struct {
	Error   string `json:"error"`
	Content string `json:"content"`
	ETag    string `json:"etag"`
}

// validationBody 是 400（配置校验不过）的响应体。
type validationBody struct {
	Error  string   `json:"error"`
	Errors []string `json:"errors"`
}

// providerItem 是 GET /api/providers 的列表项。
type providerItem struct {
	Name             string   `json:"name"`
	Note             string   `json:"note"`
	Order            int      `json:"order"`
	HasQuota         bool     `json:"has_quota"`
	HasCredential    bool     `json:"has_credential"`
	Error            string   `json:"error,omitempty"`
	ValidationErrors []string `json:"validation_errors,omitempty"`
}

// createProviderRequest 是 POST /api/providers 的请求体。
//
// 两种建法：
//   - {"template": "deepseek", "name": "x"} —— 用模板的 JSON 起头
//   - {"name": "x", "content": "{...}"} —— 直接给原文
type createProviderRequest struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	Content  string `json:"content"`
}

// handleAPIProviders 处理 GET /api/providers：列出全部 provider。
func (s *Server) handleAPIProviders(w http.ResponseWriter, r *http.Request) {
	pm := s.opts.Providers
	if pm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider 管理不可用")
		return
	}
	infos, err := pm.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]providerItem, 0, len(infos))
	for _, info := range infos {
		items = append(items, providerItem{
			Name:             info.Name,
			Note:             info.Note,
			Order:            info.Order,
			HasQuota:         info.HasQuota,
			HasCredential:    info.HasCredential,
			Error:            info.Error,
			ValidationErrors: info.ValidationErrors,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

// handleAPIProvider 处理 GET /api/providers/{name}：读一个 provider 的原文。
func (s *Server) handleAPIProvider(w http.ResponseWriter, r *http.Request) {
	data, ok := s.readProviderFile(w, r.PathValue("name"))
	if !ok {
		return
	}
	etag := etagOf(data)
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, fileEnvelope{Content: string(data), ETag: etag})
}

// handleAPIProviderPut 处理 PUT /api/providers/{name}：覆盖一个 provider。
//
// 顺序：乐观锁 → 解析请求体 → 校验 → 备份 → 原子写（校验与备份在
// provider.Manager.SaveRaw 里）。
func (s *Server) handleAPIProviderPut(w http.ResponseWriter, r *http.Request) {
	pm := s.opts.Providers
	if pm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider 管理不可用")
		return
	}
	name := r.PathValue("name")
	path, ok := providerPath(pm.ProvidersDir(), name)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "provider 不存在")
		return
	}
	cur, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "provider 不存在")
		} else {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
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
	if err := pm.SaveRaw(name, []byte(body.Content)); err != nil {
		var ve *provider.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusBadRequest, validationBody{
				Error:  "配置校验失败，改动没有写入",
				Errors: ve.Errors,
			})
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	newData, err := os.ReadFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	etag := etagOf(newData)
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, fileEnvelope{Content: string(newData), ETag: etag})
}

// handleAPIProviderCreate 处理 POST /api/providers：新建一个 provider。
func (s *Server) handleAPIProviderCreate(w http.ResponseWriter, r *http.Request) {
	pm := s.opts.Providers
	if pm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider 管理不可用")
		return
	}

	var body createProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if !validProviderName(name) {
		writeJSONError(w, http.StatusBadRequest, "provider 名不合法（只允许字母、数字、点、下划线、连字符）")
		return
	}
	if pm.Exists(name) {
		writeJSONError(w, http.StatusConflict, "provider 已存在")
		return
	}

	content := body.Content
	if strings.TrimSpace(content) == "" {
		tpl, ok := templates.ByName(body.Template)
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "未知模板或没有提供内容")
			return
		}
		content = tpl.Body
	}
	if err := pm.SaveRaw(name, []byte(content)); err != nil {
		var ve *provider.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusBadRequest, validationBody{
				Error:  "配置校验失败，没有创建",
				Errors: ve.Errors,
			})
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	path, _ := providerPath(pm.ProvidersDir(), name)
	data, _ := os.ReadFile(path)
	etag := etagOf(data)
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, fileEnvelope{Content: string(data), ETag: etag})
}

// handleAPIProviderCopy 处理 POST /api/providers/{name}/copy：复制一份。
func (s *Server) handleAPIProviderCopy(w http.ResponseWriter, r *http.Request) {
	pm := s.opts.Providers
	if pm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider 管理不可用")
		return
	}
	var body createProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	newName := strings.TrimSpace(body.Name)
	if !validProviderName(newName) {
		writeJSONError(w, http.StatusBadRequest, "provider 名不合法")
		return
	}
	if pm.Exists(newName) {
		writeJSONError(w, http.StatusConflict, "provider 已存在")
		return
	}

	src, ok := s.readProviderFile(w, r.PathValue("name"))
	if !ok {
		return
	}
	if err := pm.SaveRaw(newName, src); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	path, _ := providerPath(pm.ProvidersDir(), newName)
	data, _ := os.ReadFile(path)
	etag := etagOf(data)
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, fileEnvelope{Content: string(data), ETag: etag})
}

// handleAPIProviderDelete 处理 DELETE /api/providers/{name}：删一个 provider。
//
// 连带清掉额度数据文件里它的条目 —— 不然删掉的 provider 还留着额度数据，
// 看着像"这个 provider 还在用"。
func (s *Server) handleAPIProviderDelete(w http.ResponseWriter, r *http.Request) {
	pm := s.opts.Providers
	if pm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider 管理不可用")
		return
	}
	name := r.PathValue("name")
	// 名字先过白名单 + 确认文件在（删除没有"读-改-写"的中间态，
	// 直接按路径判存在性比按错误文案判稳）。
	if !validProviderName(name) {
		writeJSONError(w, http.StatusNotFound, "provider 不存在")
		return
	}
	path, ok := providerPath(pm.ProvidersDir(), name)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "provider 不存在")
		return
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		writeJSONError(w, http.StatusNotFound, "provider 不存在")
		return
	}
	if err := pm.Delete(name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.pruneQuotaEntries()
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}

// pruneQuotaEntries 清掉额度数据文件里"配置已不存在"的 provider 条目。
//
// live 集合 = **当前存在的全部 provider**（不是"有额度的"）——
// "这轮没刷它" ≠ "它被删了"，两件事不能混。
func (s *Server) pruneQuotaEntries() {
	if s.opts.QuotaStore == nil || s.opts.Providers == nil {
		return
	}
	infos, err := s.opts.Providers.List()
	if err != nil {
		return
	}
	live := make(map[string]bool, len(infos))
	for _, info := range infos {
		live[info.Name] = true
	}
	_ = s.opts.QuotaStore.Prune(live)
}

// readProviderFile 读一个 provider 的原文；出错时自己写响应并返回 false。
func (s *Server) readProviderFile(w http.ResponseWriter, name string) ([]byte, bool) {
	pm := s.opts.Providers
	if pm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "provider 管理不可用")
		return nil, false
	}
	path, ok := providerPath(pm.ProvidersDir(), name)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "provider 不存在")
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "provider 不存在")
		} else {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return nil, false
	}
	return data, true
}

// decodeWriteEnvelope 解析写接口的请求体。
func (s *Server) decodeWriteEnvelope(w http.ResponseWriter, r *http.Request) (writeEnvelope, bool) {
	var body writeEnvelope
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return writeEnvelope{}, false
	}
	return body, true
}
