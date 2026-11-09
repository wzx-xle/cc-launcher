package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"cc-launcher/internal/quota"
)

// quotaResponse 是 GET /api/providers/{name}/quota 的响应。
//
// **Found 是"有没有采到过额度"的标志**，不是"有没有额度限制"：
//
//   - Found=false + HasQuota=true  → 近期无活动，没采到（**不等于无限额**）
//   - Found=false + HasQuota=false → 这个 provider 压根没有额度接口
//
// 把这两种"没有"分开，是因为它们对用户的含义完全不同：前者是"不知道"，
// 后者是"没有"。混成一个"无限额"会让人放心地把量用爆。
type quotaResponse struct {
	Provider   string            `json:"provider"`
	Found      bool              `json:"found"`
	HasQuota   bool              `json:"has_quota"`
	Stale      bool              `json:"stale"`
	Source     string            `json:"source"`
	FetchedAt  int64             `json:"fetched_at"`
	Dimensions []quota.Dimension `json:"dimensions"`
}

// refreshTimeout 是手动刷新额度的超时上限。
//
// 与 `ccl quota --refresh` 的 30 秒一致：厂商的额度接口有时很慢，
// 30 秒是"慢但还活着"与"已经挂了"的分界。
const refreshTimeout = 30 * time.Second

// handleAPIProviderQuota 处理 GET /api/providers/{name}/quota：读当前额度。
//
// **零网络**：只读本地额度数据文件。serve 每 60 秒写一次，网页跟着轮询。
func (s *Server) handleAPIProviderQuota(w http.ResponseWriter, r *http.Request) {
	pm := s.opts.Providers
	if pm == nil || s.opts.QuotaStore == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "额度功能不可用")
		return
	}
	name := r.PathValue("name")
	// 名字先过白名单：quota.LoadProviderRef 直接把名字拼成路径读文件，
	// 它自己不校验名字 → 这里不挡就等于让它去读 providers 目录外的 .json。
	if !validProviderName(name) {
		writeJSONError(w, http.StatusNotFound, "provider 不存在")
		return
	}

	ref, hasQuota, err := quota.LoadProviderRef(pm.ProvidersDir(), name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "provider 不存在")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !hasQuota {
		writeJSON(w, http.StatusOK, quotaResponse{
			Provider:   name,
			Found:      false,
			HasQuota:   false,
			Dimensions: []quota.Dimension{},
		})
		return
	}

	file, err := s.opts.QuotaStore.Load()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entry, ok := file.EntryFor(name)
	if !ok {
		// 没有记录：serve 只刷新"最近有活动"的 provider，没活动的就没数据。
		writeJSON(w, http.StatusOK, quotaResponse{
			Provider:   name,
			Found:      false,
			HasQuota:   true,
			Dimensions: []quota.Dimension{},
		})
		return
	}

	dims := entry.Dimensions
	if dims == nil {
		dims = []quota.Dimension{}
	}
	writeJSON(w, http.StatusOK, quotaResponse{
		Provider:   name,
		Found:      true,
		HasQuota:   true,
		Stale:      isStale(entry.FetchedAt, ref),
		Source:     entry.Source,
		FetchedAt:  entry.FetchedAt,
		Dimensions: dims,
	})
}

// handleAPIProviderQuotaRefresh 处理 POST /api/providers/{name}/quota/refresh：
// 联网查一次额度并写回数据文件。
//
// 与 serve 的周期刷新不同：**不受"只刷活跃 provider"的限制**——
// 用户点哪个就查哪个（按需刷新）。
//
// 但**必须遵守退避**：被厂商限流（429）后退避状态是持久化的，
// 手动刷不能冲掉它——它打的是同一个接口、同一把 key。
func (s *Server) handleAPIProviderQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	pm := s.opts.Providers
	if pm == nil || s.opts.QuotaStore == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "额度功能不可用")
		return
	}
	name := r.PathValue("name")
	// 同上：名字先过白名单（LoadProviderRef 自己不校验）。
	if !validProviderName(name) {
		writeJSONError(w, http.StatusNotFound, "provider 不存在")
		return
	}

	ref, hasQuota, err := quota.LoadProviderRef(pm.ProvidersDir(), name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "provider 不存在")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !hasQuota {
		writeJSONError(w, http.StatusBadRequest, "这个 provider 没有额度接口")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	defer cancel()
	dims, err := quota.RefreshOne(ctx, ref, s.opts.QuotaStore)
	if err != nil {
		var bo *quota.BackoffError
		if errors.As(err, &bo) {
			wait := bo.RetryAfter - time.Now().Unix()
			if wait < 0 {
				wait = 0
			}
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":       "厂商限流中，暂时不能刷新",
				"retry_after": wait,
			})
			return
		}
		if errors.Is(err, quota.ErrNoQuotaInterface) {
			writeJSONError(w, http.StatusBadRequest, "这个 provider 没有额度接口")
			return
		}
		// 失败时**不写数据文件**（旧条目保留 → 慢慢变 stale）
		writeJSONError(w, http.StatusBadGateway, "刷新失败: "+err.Error())
		return
	}

	if dims == nil {
		dims = []quota.Dimension{}
	}
	resp := quotaResponse{
		Provider:   name,
		Found:      true,
		HasQuota:   true,
		Stale:      false,
		Dimensions: dims,
	}
	if file, err := s.opts.QuotaStore.Load(); err == nil {
		if entry, ok := file.EntryFor(name); ok {
			resp.Source = entry.Source
			resp.FetchedAt = entry.FetchedAt
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// isStale 判断额度记录是否过期。
//
// 阈值 = 该 provider 的刷新周期（兜底 60 秒）+ 1 秒容忍
// （quota.StaleTolerance：fetched_at 只有秒精度，取整误差最多 +1 秒）。
func isStale(fetchedAt int64, ref quota.ProviderRef) bool {
	interval := quota.DefaultRefreshInterval
	if ref.Config != nil {
		interval = ref.Config.Interval()
	}
	return time.Since(time.Unix(fetchedAt, 0)) > interval+quota.StaleTolerance
}
