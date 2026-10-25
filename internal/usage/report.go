package usage

import (
	"sort"
	"strings"
)

// Price 是模型的单价，单位 美元 / 百万 token。四个数照搬各家的计价方式：输入、输出、缓存读、缓存写。
type Price struct {
	In, Out, CacheRead, CacheWrite float64
}

// PriceInfo 是价格查询的结果。
type PriceInfo struct {
	Price Price
	// Known 为 false 表示没有价格：这些调用不计入花费，也不当作免费。
	Known bool
	// Plan 为 true 表示按套餐额度而不是按量计费（ChatGPT 账号登录），只显示 token，不计金额。
	Plan bool
}

// PriceFunc 按 (配置档, 模型) 查价格。
type PriceFunc func(profile, model string) PriceInfo

// Cost 计算一行的费用（美元）。输入口径是"含缓存读写"，所以普通输入 = 输入 − 缓存读 − 缓存写。
func Cost(p Price, r Row) float64 {
	plain := r.Input - r.CacheRead - r.CacheWrite
	if plain < 0 {
		plain = 0
	}
	return (float64(plain)*p.In + float64(r.CacheRead)*p.CacheRead + float64(r.CacheWrite)*p.CacheWrite + float64(r.Output)*p.Out) / 1e6
}

// Totals 是一组行的汇总。
type Totals struct {
	Input       int64   `json:"input"`
	Output      int64   `json:"output"`
	CacheRead   int64   `json:"cache_read"`
	CacheWrite  int64   `json:"cache_write"`
	Requests    int     `json:"requests"`
	FailedTries int     `json:"failed_tries"`
	HandedOn    int     `json:"handed_on"`
	CostUSD     float64 `json:"cost_usd"`
	// UnpricedCalls 是没有价格、因而没有计入花费的成功调用数。
	UnpricedCalls int `json:"unpriced_calls"`
	// PlanCalls 是按套餐计费、不计金额的成功调用数。
	PlanCalls int `json:"plan_calls"`
	// CacheHit 是缓存命中率：缓存读 / 输入；没有输入时为 0。
	CacheHit float64 `json:"cache_hit"`
}

func (t *Totals) add(r Row, pi PriceInfo) {
	t.Input += r.Input
	t.Output += r.Output
	t.CacheRead += r.CacheRead
	t.CacheWrite += r.CacheWrite
	t.Requests += r.Requests
	t.FailedTries += r.FailedTries
	t.HandedOn += r.HandedOn
	switch {
	case pi.Plan:
		t.PlanCalls += r.Requests
	case !pi.Known:
		t.UnpricedCalls += r.Requests
	default:
		t.CostUSD += Cost(pi.Price, r)
	}
}

func (t *Totals) finish() {
	if t.Input > 0 {
		t.CacheHit = float64(t.CacheRead) / float64(t.Input)
	}
}

// Node 是报表树里的一个节点：一天 / 一个会话 / 一个配置档 / 一个模型。
type Node struct {
	// Key 是稳定的标识（日期、会话 ID、"配置档/模型"）；Label 是显示名。
	Key   string `json:"key"`
	Label string `json:"label"`
	Totals
	// Plan 为 true 表示叶子节点是套餐计费（界面上花费一栏显示"套餐"）。
	Plan bool `json:"plan,omitempty"`
	// Priced 为 false 表示叶子节点没有价格（花费一栏显示"—"）。
	Priced   bool   `json:"priced"`
	Children []Node `json:"children,omitempty"`
}

// Report 是给界面的完整报表。
type Report struct {
	By     string `json:"by"`
	Totals Totals `json:"totals"`
	Nodes  []Node `json:"nodes"`
	// UnpricedModels 列出没有价格的 "配置档/模型"，界面据此提示去补价格。
	UnpricedModels []string `json:"unpriced_models"`
}

// Build 把聚合行整理成报表树并计价。rows 的顺序决定同级节点的顺序（Aggregate 已经排好）。
func Build(rows []Row, by string, price PriceFunc) Report {
	rep := Report{By: by, Nodes: []Node{}, UnpricedModels: []string{}}
	unpriced := map[string]bool{}
	index := map[string]int{} // 顶层节点 key → 下标

	leaf := func(r Row) (Node, PriceInfo) {
		pi := price(r.Profile, r.Model)
		n := Node{Key: r.Profile + "/" + r.Model, Label: r.Profile + " / " + r.Model, Plan: pi.Plan, Priced: pi.Known && !pi.Plan}
		n.Totals.add(r, pi)
		n.finish()
		if !pi.Known && !pi.Plan && r.Requests > 0 {
			unpriced[n.Key] = true
		}
		return n, pi
	}

	for _, r := range rows {
		l, pi := leaf(r)
		rep.Totals.add(r, pi)

		var topKey, topLabel string
		switch by {
		case BySession:
			topKey, topLabel = r.SessionID, r.SessionTitle
			if topLabel == "" {
				topLabel = "（无标题）"
				if r.SessionID == "" {
					topLabel = "（未关联会话）"
				}
			}
		case ByModel:
			rep.Nodes = append(rep.Nodes, l)
			continue
		case ByProfile:
			topKey, topLabel = r.Profile, r.Profile
		default:
			topKey, topLabel = r.Day, r.Day
		}
		i, ok := index[topKey]
		if !ok {
			rep.Nodes = append(rep.Nodes, Node{Key: topKey, Label: topLabel})
			i = len(rep.Nodes) - 1
			index[topKey] = i
		}
		top := &rep.Nodes[i]
		top.Totals.add(r, pi)
		top.Children = append(top.Children, l)
	}
	for i := range rep.Nodes {
		rep.Nodes[i].finish()
		sort.SliceStable(rep.Nodes[i].Children, func(a, b int) bool {
			c := rep.Nodes[i].Children
			return c[a].Input+c[a].Output > c[b].Input+c[b].Output // 用得多的排前面
		})
	}
	if by == ByModel {
		sort.SliceStable(rep.Nodes, func(a, b int) bool {
			return rep.Nodes[a].Input+rep.Nodes[a].Output > rep.Nodes[b].Input+rep.Nodes[b].Output
		})
	}
	rep.Totals.finish()
	for k := range unpriced {
		rep.UnpricedModels = append(rep.UnpricedModels, k)
	}
	sort.Strings(rep.UnpricedModels)
	return rep
}

// SplitKey 把 "配置档/模型" 拆开（模型名里可能带 /，只按第一个 / 拆）。
func SplitKey(k string) (profile, model string) {
	profile, model, _ = strings.Cut(k, "/")
	return
}
