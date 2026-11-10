package ecommerceagent

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
)

func (p *designPicker) orderMiddle(prev string, items []designOption) []designOption {
	work := append([]designOption(nil), items...)
	if len(work) < 2 {
		return work
	}
	for attempt := 0; attempt < 200; attempt++ {
		p.r.Shuffle(len(work), func(i, j int) { work[i], work[j] = work[j], work[i] })
		if componentRepeats(prev, work) == 0 {
			return work
		}
	}
	remaining := work
	ordered := make([]designOption, 0, len(work))
	for len(remaining) > 0 {
		counts := map[string]int{}
		for _, item := range remaining {
			counts[designComponent(item.text)]++
		}
		best := -1
		for i, item := range remaining {
			component := designComponent(item.text)
			if component == prev {
				continue
			}
			if best < 0 || counts[component] > counts[designComponent(remaining[best].text)] {
				best = i
			}
		}
		if best < 0 {
			best = 0
		}
		ordered = append(ordered, remaining[best])
		prev = designComponent(remaining[best].text)
		remaining = slices.Delete(remaining, best, best+1)
	}
	return ordered
}

func componentRepeats(prev string, items []designOption) int {
	repeats := 0
	for _, item := range items {
		component := designComponent(item.text)
		if component == prev {
			repeats++
		}
		prev = component
	}
	return repeats
}

// designComponent is the informationComponent category a detail layout starts with.
func designComponent(text string) string {
	if before, _, ok := strings.Cut(text, "｜"); ok {
		return before
	}
	return ""
}

func (p *designPicker) allowed(o designOption, level int) bool {
	if (o.has("people") && !p.people) || (o.has("multi") && !p.multi) {
		return false
	}
	if o.has("x:" + p.family) {
		return false
	}
	platforms, platformMatch := designTagMatch(o.tags, "p:", p.family)
	categories, categoryMatch := designTagMatch(o.tags, "c:", p.category)
	styles, styleMatch := designTagMatch(o.tags, "s:", p.style)
	if platforms && !platformMatch {
		return false
	}
	if level < 1 && categories && !categoryMatch {
		return false
	}
	if level < 2 && p.style != "" && styles && !styleMatch {
		return false
	}
	return level >= 3 || !p.noText || !o.has("text")
}

func designTagMatch(tags []string, prefix, wanted string) (present, matched bool) {
	for _, tag := range tags {
		if name, ok := strings.CutPrefix(tag, prefix); ok {
			present, matched = true, matched || name == wanted
		}
	}
	return present, matched
}

// candidates relaxes the category fit, then the style fit, then the no-text
// preference, until the pool has n entries that neither a recent run nor this
// run used. People, multi-variant and platform fit are never relaxed.
func (p *designPicker) candidates(pool []designOption, n int) []designOption {
	var list []designOption
	for level := 0; level <= 3; level++ {
		list = list[:0]
		fresh := 0
		for _, o := range pool {
			if p.allowed(o, level) {
				list = append(list, o)
				if !p.avoid[o.id] && !p.used[o.id] {
					fresh++
				}
			}
		}
		if fresh >= n {
			return list
		}
	}
	if len(list) > 0 {
		return list
	}
	for _, o := range pool {
		if (p.people || !o.has("people")) && (p.multi || !o.has("multi")) {
			list = append(list, o)
		}
	}
	if len(list) > 0 {
		return list
	}
	return pool
}

// tiers splits entries into fresh ones, ones a recent run used, and ones this
// run already drew, so draws prefer the first and repeat only when needed.
func (p *designPicker) tiers(list []designOption) [3][]designOption {
	var tiers [3][]designOption
	for _, o := range list {
		switch {
		case p.used[o.id]:
			tiers[2] = append(tiers[2], o)
		case p.avoid[o.id]:
			tiers[1] = append(tiers[1], o)
		default:
			tiers[0] = append(tiers[0], o)
		}
	}
	return tiers
}

func (p *designPicker) pick(pool []designOption, n int) []designOption {
	if n <= 0 || len(pool) == 0 {
		return nil
	}
	var order []designOption
	for _, tier := range p.tiers(p.candidates(pool, n)) {
		p.weightedShuffle(tier)
		order = append(order, tier...)
	}
	out := make([]designOption, 0, n)
	for i := 0; i < n; i++ {
		o := order[i%len(order)]
		p.used[o.id] = true
		out = append(out, o)
	}
	return out
}

func (p *designPicker) pickOne(pool []designOption) (designOption, bool) {
	if picked := p.pick(pool, 1); len(picked) == 1 {
		return picked[0], true
	}
	return designOption{}, false
}

// pickPreferred draws one entry, weighting entries made for the platform
// family three times higher.
func (p *designPicker) pickPreferred(pool []designOption) (designOption, bool) {
	for _, tier := range p.tiers(p.candidates(pool, 1)) {
		total := 0
		for _, o := range tier {
			total += p.weight(o)
		}
		if total == 0 {
			continue
		}
		x := p.r.IntN(total)
		for _, o := range tier {
			if x -= p.weight(o); x < 0 {
				p.used[o.id] = true
				return o, true
			}
		}
	}
	return designOption{}, false
}

// weight favours entries made for the platform family three to one.
func (p *designPicker) weight(o designOption) int {
	if o.has("f:"+p.family) || o.has("p:"+p.family) {
		return 3
	}
	return 1
}

// weightedShuffle orders entries randomly, drawing platform-specific entries
// earlier, so they are likelier to be picked without crowding out the rest.
func (p *designPicker) weightedShuffle(list []designOption) {
	for i := range list {
		total := 0
		for _, o := range list[i:] {
			total += p.weight(o)
		}
		x := p.r.IntN(total)
		for j := i; j < len(list); j++ {
			if x -= p.weight(list[j]); x < 0 {
				list[i], list[j] = list[j], list[i]
				break
			}
		}
	}
}

// pickCasting prefers the casting of the exact target market, then any
// market-neutral casting.
func (p *designPicker) pickCasting() (designOption, bool) {
	var market, neutral []designOption
	for _, o := range designCastingModifiers {
		marked := false
		for _, tag := range o.tags {
			if id, ok := strings.CutPrefix(tag, "id:"); ok {
				marked = true
				if id == p.platform {
					market = append(market, o)
				}
			}
		}
		if !marked {
			neutral = append(neutral, o)
		}
	}
	if len(market) > 0 {
		return p.pickOne(market)
	}
	return p.pickOne(neutral)
}

func (p *designPicker) shuffleInts(values []int) {
	p.r.Shuffle(len(values), func(i, j int) { values[i], values[j] = values[j], values[i] })
}

func taggedLayouts(pool []designOption, tag string) []designOption {
	var out []designOption
	for _, o := range pool {
		if o.has(tag) {
			out = append(out, o)
		}
	}
	return out
}

func prefixedLayouts(prefix string, pool []designOption) []designOption {
	out := make([]designOption, len(pool))
	for i, o := range pool {
		out[i] = designOption{id: o.id, text: prefix + o.text, tags: o.tags}
	}
	return out
}

// repeatedLayout gives every image the same layout, for types whose images must
// match (SKU sets, three-view sets).
func repeatedLayout(picked []designOption, count int) []designOption {
	if len(picked) == 0 {
		return nil
	}
	out := make([]designOption, count)
	for i := range out {
		out[i] = picked[0]
	}
	return out
}

func designSignature(ids []string) string {
	h := fnv.New64a()
	for _, id := range ids {
		h.Write([]byte(id + "|"))
	}
	return fmt.Sprintf("%012x", h.Sum64()&0xffffffffffff)
}

// DetailOutline returns the drawn page layouts for the parallel detail
// planning batches, so every batch shares this run's outline.
func (v DesignVariation) DetailOutline() []string {
	return append([]string(nil), v.Layouts...)
}

// PromptBlock is the planning prompt's last block: the drawn layouts, UI kit,
// typography, persona and scenes, ranked above the fixed examples earlier in
// the prompt.
func (v DesignVariation) PromptBlock(imageType string) string {
	kind := strings.TrimSpace(imageType)
	var b strings.Builder
	b.WriteString("【本轮版式方案·" + DesignPlaybookVersion + "】\n")
	b.WriteString("本方案由服务端为本轮随机抽取（编号 " + v.Seed + "），同一风格再次生成也会换一套版式、UI 元素与人物。它优先于前文所有固定的版式、机位与人物示例：逐张分镜约束与整套内容分镜、九屏与页面大纲示例、头部品牌详情页的骨架示例、按品类给出的模特人设与字体示例、“最后一屏上图下卡”的固定写法。已选方向的配色、光线、视觉锁定句与整体气质，商品真实性与平台规则，以及用户明确指定的内容、无文字、白底和不要人物等要求，仍然高于本方案")
	if v.UIKit != "" {
		b.WriteString("；UI 套件、字体与场景在已选方向的气质内落地，颜色与质感跟随方向")
	}
	b.WriteString("。\n")
	same := len(v.Layouts) > 1
	for _, layout := range v.Layouts {
		same = same && layout == v.Layouts[0]
	}
	switch {
	case same:
		fmt.Fprintf(&b, "全部 %d 张使用同一版式，只换真实的 SKU 或视角，不新增颜色或款式：%s\n", len(v.Layouts), v.Layouts[0])
	case len(v.Layouts) > 0:
		if kind == "detail" {
			b.WriteString("逐屏版式：第 N 屏的 composition、informationComponent 与 prompt 必须落实对应版式；informationComponent 以版式开头的分类开头，再写清位置、层级、间距与手机端可读性；可按商品细化位置、文案与道具，但不换成别的骨架。\n")
		} else {
			b.WriteString("逐张版式：第 N 张的 composition 与 prompt 必须落实对应版式；可按商品细化位置、文案与道具，但不换成别的骨架，相邻两张不退回同一构图。\n")
		}
		for i, layout := range v.Layouts {
			fmt.Fprintf(&b, "图%d：%s\n", i+1, layout)
		}
	}
	if v.UIKit != "" {
		b.WriteString("UI 套件（标签、图标、引线与放大窗、编号与卡片、装饰整套统一）：" + v.UIKit + "\n")
	}
	if v.Typography != "" {
		b.WriteString("字体（整套统一，优先于前文按品类给出的字体示例；跨境平台用目标语言的同气质字形）：" + v.Typography + "\n")
	}
	if v.Persona != "" {
		b.WriteString("人物设定（整套同一角色，只在版式需要人物的页面出现，各页换动作、景别与场景；用户启用模特并上传模特图时以模特图为准，用户要求不要人物时不出现）：" + v.Persona + "\n")
	}
	if len(v.Scenes) > 0 {
		b.WriteString("场景库（按页取用，不同页面用不同场景，改写成适合本商品与已选方向布景的具体画面）：" + strings.Join(v.Scenes, "；") + "\n")
	}
	if v.UIKit == "" {
		b.WriteString("本轮全部是纯白合规画面：纯白背景、无道具、无新增文字与图形，只执行上述角度、阴影、布光与陈列差异；visualStrategy 写明逐张的角度与光影安排。")
		return b.String()
	}
	if kind == "detail" {
		b.WriteString("商品状态按各屏括号内的提示轮换，并与【商品状态多样化】一致。")
	}
	named := []string{"UI 套件"}
	if v.Typography != "" {
		named = append(named, "字体")
	}
	if v.Persona != "" {
		named = append(named, "人物设定")
	}
	b.WriteString("用户选择无文字时，版式中的标题、标签与编号区改为留白，不添加任何文字。visualStrategy 必须写明本方案的 " + strings.Join(named, "、") + "，并说明逐张版式如何落实。")
	return b.String()
}
