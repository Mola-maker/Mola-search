// The 30 search modes (M01~M30): each one a different way of asking, not a
// different badge. A mode's plan says what sub-queries to spin, how many
// hops to walk, whether to iterate on gaps or novelty, whether to argue
// both sides, and what to check beyond text (weather, images, video, DOM).
package searchdet

import (
	"embed"
	"encoding/json"
	"strings"
)

//go:embed modes_catalog.json
var catalogFS embed.FS

// rawMode is the catalog's own shape (modes_catalog.json), kept apart from
// the served shape so the file stays the source of truth.
type rawMode struct {
	ID                string   `json:"id"`
	Slug              string   `json:"slug"`
	MCPTool           string   `json:"mcpTool"`
	Family            string   `json:"family"`
	FamilyName        string   `json:"familyName"`
	Name              string   `json:"name"`
	NameEn            string   `json:"nameEn"`
	Description       string   `json:"description"`
	Modalities        []string `json:"modalities"`
	AlgorithmTopology string   `json:"algorithmTopology"`
	LatencyTargetMs   int      `json:"latencyTargetMs"`
}

// Catalog is the 30 modes, in the shape the page renders: the catalog's own
// names plus the legacy fields the Detective page reads.
func Catalog() []SearchModeItem {
	raw, err := catalogFS.ReadFile("modes_catalog.json")
	if err != nil {
		return nil
	}
	var parsed struct {
		Modes []rawMode `json:"modes"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return nil
	}
	out := make([]SearchModeItem, 0, len(parsed.Modes))
	for _, m := range parsed.Modes {
		out = append(out, SearchModeItem{
			ID: m.ID, Slug: m.Slug, Family: m.Family,
			NameZh: m.Name, NameEn: m.NameEn,
			Topology:      m.AlgorithmTopology,
			LatencyBudget: itoa(m.LatencyTargetMs) + " ms",
			Suppliers:     m.Modalities,
		})
	}
	return out
}

// ModeOf is one mode by id ("" when unknown — the caller falls back to M01).
func ModeOf(id string) (SearchModeItem, bool) {
	for _, m := range Catalog() {
		if m.ID == id {
			return m, true
		}
	}
	return SearchModeItem{}, false
}

// subQuery is one question the mode asks, with the role it plays.
type subQuery struct {
	Role string
	Q    string
}

// modePlan is what a mode really does differently: which questions, how
// deep, what iteration, what checks. The names and topologies are the
// catalog's (modes_catalog.json), so a mode's badge and its work agree.
type modePlan struct {
	Queries     []subQuery // the first round's questions
	Hops        int        // entity-driven second-hop searches
	ProCon      bool       // adversarial counter-query round
	GapRounds   int        // AREX: re-ask what stayed unanswered
	NoveltyStop bool       // RAAC: stop when new information dries up
	SelfRAGGate bool       // refuse to answer without support
	Temporal    bool       // build the time-evolution tree
	Graph       bool       // keep entity co-occurrence edges
	AuthBoost   bool       // weight government/first-party sources up
	FreshOnly   bool       // prefer what was published lately
	NoFold      bool       // keep channels apart (the compare mode)
	NoPersist   bool       // zero-retention mode: nothing is written
	HyDE        bool       // draft a hypothetical answer and search with it
	Snowball    bool       // follow the citations' domains for primary sources
	Multimodal  []string   // cards: weather | image | video | dom | adb
	Note        string     // what the plan is, shown in the trajectory
}

// planFor maps a mode id to what it does. Unknown ids run M01.
func planFor(modeID, query string) modePlan {
	base := modePlan{Queries: []subQuery{{Role: "主查询", Q: query}}, Note: "直接检索"}
	switch modeID {
	case "M01": // direct fast
		base.Note = "直通检索：单查询 + 隐私脱敏 + 权威排序"
	case "M02": // hybrid RAG: sparse and dense variants fused
		base.Queries = append(base.Queries,
			subQuery{"稀疏召回", query + " 定义 说明"},
			subQuery{"稠密召回", query + " 相关 讨论 分析"})
		base.Note = "混合检索：稀疏(BM25 变体) + 稠密(语义变体) 双路 RRF 融合"
	case "M03": // HyDE
		base.HyDE = true
		base.Queries = append(base.Queries, subQuery{"假设答案", "关于「" + coreSubject(query) + "」的答案要点与结论"})
		base.Note = "HyDE：先生成假设性答案草稿，再用它召回证据"
	case "M04": // multi-query expansion
		base.Queries = append(base.Queries,
			subQuery{"变体A", query + " 官方 通报"},
			subQuery{"变体B", query + " 实测 数据"},
			subQuery{"变体C", query + " 专家 解读"})
		base.Note = "多查询改写：4 路变体并行召回后融合"
	case "M05": // heuristic MCTS: branch, score, expand the best
		base.Queries = append(base.Queries,
			subQuery{"分支·权威", query + " 官方 通报"},
			subQuery{"分支·实证", query + " 实测 数据"},
			subQuery{"分支·反证", query + " 辟谣 质疑"})
		base.GapRounds = 1
		base.AuthBoost = true
		base.Note = "启发式 MCTS：按权威×新颖度给分支打分，扩写最优分支"
	case "M06": // multi-hop bridge: two rounds through entities
		base.Hops = 2
		base.Note = "多跳桥接：实体抽取后两轮二次检索"
	case "M07": // LightRAG dual-level graph
		base.Graph = true
		base.Queries = append(base.Queries,
			subQuery{"低层实体", query + " 关联 主体"},
			subQuery{"高层社区", query + " 背景 综述"})
		base.Note = "LightRAG 双层级图：实体 1 跳 + 社区摘要"
	case "M08": // citation snowball: follow primary sources
		base.Snowball = true
		base.Note = "引文雪球：从返回结果的域名继续追一手来源"
	case "M09": // AREX gap iteration
		base.GapRounds = 2
		base.Queries = append(base.Queries, subQuery{"分解1", "什么是 " + coreSubject(query)}, subQuery{"分解2", query + " 争议 分歧"})
		base.Note = "AREX 信息缺口迭代追问（两轮）"
	case "M10": // RAAC novelty convergence
		base.NoveltyStop = true
		base.Hops = 2
		base.Queries = append(base.Queries, subQuery{"扩展", query + " 最新 进展"})
		base.Note = "RAAC 新颖度边际增益收敛判定"
	case "M11": // Self-RAG abstain gate
		base.SelfRAGGate = true
		base.Note = "Self-RAG 支撑度评估 + ABSTAIN 拒答门控"
	case "M12": // DeepRAG tree: decompose and solve sub-questions
		base.GapRounds = 1
		base.Hops = 1
		base.Queries = append(base.Queries, subQuery{"子问题", coreSubject(query) + " 子问题 拆解"})
		base.Note = "DeepRAG 递归分解：子问题树两轮求解"
	case "M13": // adversarial pro/con
		base.ProCon = true
		base.SelfRAGGate = true
		base.Note = "正反方双路对抗反查 + SimHash 去重折叠"
	case "M14": // jury consensus: four channels, grouped by domain
		base.NoFold = true
		base.AuthBoost = true
		base.Queries = append(base.Queries,
			subQuery{"陪审·媒体", query + " 报道"},
			subQuery{"陪审·官方", query + " 官方 声明"},
			subQuery{"陪审·民间", query + " 网友 讨论"})
		base.Note = "陪审团共识：多路并行 + 域名分组 + 时间衰减裁决"
	case "M15": // creative abductive reasoning
		base.Queries = append(base.Queries,
			subQuery{"类比", "类似 " + query + " 的案例"},
			subQuery{"第一性", coreSubject(query) + " 原理 机制"})
		base.NoFold = true
		base.Note = "溯因创造：结构抽象 + 跨域类比"
	case "M16": // Popper falsification
		base.ProCon = true
		base.Note = "波普尔证伪：生成反例查询，只认可被证伪的证据"
	case "M17": // temporal tree
		base.Temporal = true
		base.Queries = append(base.Queries, subQuery{"溯源", query + " 起源 最早"}, subQuery{"演进", query + " 后续 进展 演变"})
		base.Note = "时间演化树构建（真实日期提取）"
	case "M18": // topic clustering
		base.NoFold = true
		base.Queries = append(base.Queries,
			subQuery{"主题A", coreSubject(query) + " 核心 概念"},
			subQuery{"主题B", coreSubject(query) + " 应用 场景"},
			subQuery{"主题C", coreSubject(query) + " 争议 局限"})
		base.Note = "主题聚类：多主题召回 + 域名/时间分簇"
	case "M19": // stakeout watch: freshness first
		base.FreshOnly = true
		base.Temporal = true
		base.Queries = append(base.Queries, subQuery{"时效", query + " 今天 最新消息"})
		base.Note = "盯梢巡检：Freshness=d1 强时效 + 快照差分"
	case "M20": // vertical authority
		base.AuthBoost = true
		base.Queries = append(base.Queries, subQuery{"权威", query + " 官方 site:gov.cn 通报"}, subQuery{"辟谣", query + " 辟谣 权威发布"})
		base.Note = "垂域权威：gov/学术/一手信源硬过滤加权"
	case "M21": // privacy redacted, session-only
		base.SelfRAGGate = true
		base.NoPersist = true
		base.Note = "隐私脱敏 + 零留存（L0 会话内，不落盘）"
	case "M22", "M26": // image fact-check and provenance
		base.Multimodal = append(base.Multimodal, "image")
		base.SelfRAGGate = true
		if modeID == "M26" {
			base.Queries = append(base.Queries, subQuery{"最早出处", coreSubject(query) + " 最早 出处 原始"})
			base.Note = "图片来源溯源：dHash/aHash + EXIF + 最早入库时间"
		} else {
			base.Note = "视觉事实核查：图片指纹 + EXIF + 文中主张比对"
		}
	case "M23": // code forensics
		base.Queries = append(base.Queries,
			subQuery{"代码", coreSubject(query) + " site:github.com"},
			subQuery{"议题", coreSubject(query) + " issue 讨论 error"})
		base.Note = "代码取证：GitHub 仓库/issue + 技术文章交叉"
	case "M24": // federated multi-database fan-out
		base.NoFold = true
		base.Graph = true
		base.Queries = append(base.Queries,
			subQuery{"主库视角", query},
			subQuery{"影子库视角", query + " 数据 统计"},
			subQuery{"对等视角", query + " 社区 讨论"})
		base.Note = "联邦多库扇出：全部通道 + 关系边扩展（主库/影子/对等）"
	case "M25": // geo + weather
		base.Multimodal = append(base.Multimodal, "weather")
		base.AuthBoost = true
		base.Note = "地图空间 + 实时气象核验"
	case "M27": // video
		base.Multimodal = append(base.Multimodal, "video")
		base.Note = "视频取证：B站元数据 + 弹幕 + 关键帧"
	case "M28": // DOM CU
		base.Multimodal = append(base.Multimodal, "dom")
		base.Note = "Computer-Use：DOM 无障碍树快照"
	case "M29": // ADB
		base.Multimodal = append(base.Multimodal, "dom", "adb")
		base.Note = "ADB 真机探测 + 页面无障碍树"
	case "M30": // search-model ensemble
		base.NoFold = true
		base.Queries = append(base.Queries,
			subQuery{"对照·新闻", query + " 新闻"},
			subQuery{"对照·百科", query + " 百科"},
			subQuery{"对照·论文", query + " 论文 研究"})
		base.Note = "搜索模型对照：各通道独立呈现，不做折叠"
	default:
		return planFor("M01", query)
	}
	return base
}

// boolZh renders a bool for a note.
func boolZh(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

// coreSubject strips question decorations down to the thing asked about.
func coreSubject(query string) string {
	for _, cut := range []string{"？", "?", "，", ",", "：", ":", "；", ";", "、", "。", "\n"} {
		if i := strings.Index(query, cut); i > 4 {
			return strings.TrimSpace(query[:i])
		}
	}
	return firstRunesOf(strings.TrimSpace(query), 40)
}

// verticalQueries adds topic-shaped variants: the channels a medical or
// code question needs differ from a rumor check.
func verticalQueries(query string) []subQuery {
	q := coreSubject(query)
	return []subQuery{
		{Role: "学术", Q: q + " 研究 论文"},
		{Role: "社区", Q: q + " 知乎 讨论"},
		{Role: "新闻", Q: q + " 报道 事件"},
	}
}

// antiQueries are the counter-side questions an adversarial mode asks: the
// claim plus the words that find its refutation, not its echo.
func antiQueries(query string) []subQuery {
	q := coreSubject(query)
	return []subQuery{
		{Role: "反方·辟谣", Q: q + " 辟谣"},
		{Role: "反方·真相", Q: q + " 真相 实测"},
		{Role: "反方·反转", Q: q + " 假的 反转 谣言"},
	}
}
