package quota

import (
	"encoding/json"
	"time"
)

// dimensionJSON 是 Dimension 的磁盘/线缆形状。
//
// 与 Dimension 结构体的差别（都是有意的）：
//   - Duration 用 **秒**（duration_seconds）而不是 time.Duration——线缆格式
//     要能被仓库外的消费方读，秒比纳秒直观
//   - ResetsAt / ExpiresAt 用 **Unix 秒**（而不是 RFC3339 字符串）——与
//     Claude Code 的 rate_limits.resets_at 同一形状，状态栏的渲染函数可以
//     直接复用
//   - used_percentage 是**派生**的（UsedPct()），不来自厂商响应
//   - **没有的字段不写**（不写 null）——消费方不必区分"null"和"缺失"
type dimensionJSON struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Kind     Kind           `json:"kind"`
	Duration int64          `json:"duration_seconds,omitempty"`
	UsedPct  *float64       `json:"used_percentage,omitempty"`
	Used     *float64       `json:"used,omitempty"`
	Limit    *float64       `json:"limit,omitempty"`
	Remaining *float64      `json:"remaining,omitempty"`
	Unit     string         `json:"unit,omitempty"`
	ResetsAt *int64         `json:"resets_at,omitempty"`
	ExpiresAt *int64        `json:"expires_at,omitempty"`
	Unlimited bool          `json:"unlimited,omitempty"`
	Raw      map[string]any `json:"raw,omitempty"`
}

// MarshalJSON 把 Dimension 序列化成对外形状。
func (d Dimension) MarshalJSON() ([]byte, error) {
	out := dimensionJSON{
		ID:        d.ID,
		Label:     d.Label,
		Kind:      d.Kind,
		UsedPct:   d.UsedPct(),
		Used:      d.Used,
		Limit:     d.Limit,
		Remaining: d.Remaining,
		Unit:      d.Unit,
		Unlimited: d.Unlimited,
		Raw:       d.Raw,
	}
	if d.Duration > 0 {
		out.Duration = int64(d.Duration / time.Second)
	}
	if d.ResetsAt != nil {
		v := d.ResetsAt.Unix()
		out.ResetsAt = &v
	}
	if d.ExpiresAt != nil {
		v := d.ExpiresAt.Unix()
		out.ExpiresAt = &v
	}
	return json.Marshal(out)
}

// UnmarshalJSON 从对外形状还原 Dimension。
//
// **百分比能从 used/limit 重建**；万一某个维度只有 used_percentage 没有
// used/limit（分母未知但厂商直接给了百分比），用"分母=100"反推，保证
// 存盘→读出→再存盘的往返一致（否则百分比会在往返中丢失）。
func (d *Dimension) UnmarshalJSON(data []byte) error {
	var in dimensionJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	d.ID = in.ID
	d.Label = in.Label
	d.Kind = in.Kind
	d.Used = in.Used
	d.Limit = in.Limit
	d.Remaining = in.Remaining
	d.Unit = in.Unit
	d.Unlimited = in.Unlimited
	d.Raw = in.Raw
	if in.Duration > 0 {
		d.Duration = time.Duration(in.Duration) * time.Second
	}
	if in.ResetsAt != nil {
		t := time.Unix(*in.ResetsAt, 0)
		d.ResetsAt = &t
	}
	if in.ExpiresAt != nil {
		t := time.Unix(*in.ExpiresAt, 0)
		d.ExpiresAt = &t
	}
	if in.UsedPct != nil && d.Used == nil && d.Limit == nil && !d.Unlimited {
		// 只有百分比 → 用分母 100 反推，保证往返不丢。
		d.Limit = f64(100)
		d.Used = f64(*in.UsedPct)
	}
	return nil
}
