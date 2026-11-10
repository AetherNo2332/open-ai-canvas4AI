package ecommerceagent

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

type DesignVariation struct {
	Seed       string   `json:"seed"`
	Layouts    []string `json:"layouts"`
	UIKit      string   `json:"uiKit"`
	Typography string   `json:"typography"`
	Persona    string   `json:"persona"`
	Scenes     []string `json:"scenes"`
	Signature  string   `json:"signature"`
	// LayoutSignature identifies the layout list alone, so a caller can keep
	// a product's next sets from repeating one exact list of layouts.
	LayoutSignature string `json:"layoutSignature"`
	// Picks are the ids of the drawn entries. They are not serialized; the
	// caller passes them back in VariationInput.Avoid on the next run.
	Picks []string `json:"-"`
}

// VariationInput describes the request a variation is drawn for. Category is a
// product category key (beauty, maternal, apparel, bathroom, appliance, tech,
// home, food, sport, pet, general). Style is optional text naming the selected
// direction (label and description); it keeps the UI kit and typography inside
// that style's family of the style library. MultiVariant is set when the
// product really has several colours, SKUs or pieces; without it entries
// tagged "multi" are never drawn. Avoid lists recent signatures, layout
// signatures and entry ids (DesignVariation.Picks): a draw whose signature or
// layout signature is listed is drawn again, and listed entries are skipped
// while the pool still has other fitting entries.
type VariationInput struct {
	ImageType, Platform, Category, Distribution, Purpose, Style string
	Region, Language                                            string
	Count                                                       int
	NoText, PeopleAllowed, MultiVariant                         bool
	Avoid                                                       []string
}

// PickDesignVariation draws the layouts, UI kit, typography, persona and
// scenes of one run from the playbook. Layouts fit the content type and
// platform and do not repeat within a set while the pool allows; detail pages
// open with 卖点总览 or 标题导语, close with 规格卡 or 留白收束, never repeat an
// informationComponent on adjacent pages, include at least two close-ups and,
// when people are allowed, at least three pages with people.
func PickDesignVariation(r *rand.Rand, in VariationInput) DesignVariation {
	r = designRand(r)
	avoid := make(map[string]bool, len(in.Avoid))
	for _, key := range in.Avoid {
		if key = strings.TrimSpace(key); key != "" {
			avoid[key] = true
		}
	}
	v := drawDesignVariation(r, in, avoid)
	for attempt := 1; attempt < 8 && (avoid[v.Signature] || avoid[v.LayoutSignature]); attempt++ {
		v = drawDesignVariation(r, in, avoid)
	}
	return v
}

type designPicker struct {
	r        *rand.Rand
	family   string
	platform string
	category string
	style    string
	noText   bool
	people   bool
	multi    bool
	avoid    map[string]bool
	used     map[string]bool
}

// designStyleGroup names the style-library family (静谧治愈, 电音燃动, 潮流先锋,
// 都市质感, 活力趣味) closest to a direction, or "" when nothing matches.
func designStyleGroup(style string) string {
	if strings.TrimSpace(style) == "" {
		return ""
	}
	if preset, ok := MatchStylePreset(style); ok {
		return preset.Group
	}
	return ""
}

func drawDesignVariation(r *rand.Rand, in VariationInput, avoid map[string]bool) DesignVariation {
	kind := strings.TrimSpace(in.ImageType)
	category := strings.TrimSpace(in.Category)
	if _, ok := designScenes[category]; !ok {
		category = "general"
	}
	p := &designPicker{r: r, family: platformFamily(in.Platform), platform: platformKey(in.Platform), category: category, style: designStyleGroup(in.Style), noText: in.NoText, people: in.PeopleAllowed && kind != "white" && kind != "three-view", multi: in.MultiVariant, avoid: avoid, used: map[string]bool{}}
	v := DesignVariation{Seed: fmt.Sprintf("%08x", r.Uint32())}
	layouts := p.layouts(kind, min(max(in.Count, 1), 30), in)
	picks := make([]string, 0, len(layouts)+8)
	pure, withPeople := true, false
	for _, layout := range layouts {
		v.Layouts = append(v.Layouts, layout.text)
		picks = append(picks, layout.id)
		pure = pure && layout.has("white")
		withPeople = withPeople || layout.has("people")
	}
	v.LayoutSignature = "layouts:" + designSignature(picks)
	// Pure-white images carry no UI, text, people or scene.
	if !pure {
		if kit, ok := p.pickPreferred(designUIKits); ok {
			v.UIKit, picks = kit.text, append(picks, kit.id)
		}
		if !in.NoText {
			if pairing, ok := p.pickPreferred(designTypography); ok {
				v.Typography, picks = pairing.text, append(picks, pairing.id)
			}
		}
		if p.people && (withPeople || designPersonaTypes[kind]) {
			if persona, ok := p.pickOne(designPersonas[category]); ok {
				v.Persona, picks = persona.text, append(picks, persona.id)
				market, overseas, direction := designCastingMarket(in)
				v.Persona += "；市场人物方向：" + direction
				if overseas {
					casting := *p
					casting.platform = market
					if cast, ok := casting.pickCasting(); ok {
						v.Persona, picks = v.Persona+"；"+cast.text, append(picks, cast.id)
					}
				}
				if face, ok := p.pickOne(designFaceDetails); ok {
					v.Persona, picks = v.Persona+"；"+face.text, append(picks, face.id)
				}
				v.Persona += "；按商品明确的适用人群校准年龄与性别，脸部识别点服从人物年龄和已上传参考。逐张 prompt 复述本轮人物面貌与自然肤质，不能只写漂亮模特或只改变衣服。"
			}
		}
		for _, scene := range p.pick(designScenes[category], designSceneCounts[kind]) {
			v.Scenes, picks = append(v.Scenes, scene.text), append(picks, scene.id)
		}
	}
	v.Picks, v.Signature = picks, designSignature(picks)
	return v
}

// designPersonaTypes always cast a persona when people are allowed; other
// types cast one only when a drawn layout shows people.
var designPersonaTypes = map[string]bool{"detail": true, "scene": true, "social": true, "marketing": true, "poster": true, "amazon-a-plus": true}

var designSceneCounts = map[string]int{"detail": 4, "set": 3, "secondary": 3, "scene": 3, "amazon-a-plus": 3, "main": 2, "single": 2, "marketing": 2, "poster": 2, "social": 2, "selling": 2, "feature": 2}

func (p *designPicker) layouts(kind string, count int, in VariationInput) []designOption {
	switch kind {
	case "detail":
		return p.detailLayouts(count)
	case "set":
		if count == 2 && strings.Contains(in.Distribution, "主图") && strings.Contains(in.Distribution, "副图") {
			return append(p.pick(p.mainPool(), 1), p.pick(designSecondaryLayouts, 1)...)
		}
		return p.setLayouts(count)
	case "main":
		return p.pick(p.mainPool(), count)
	case "secondary", "selling", "feature":
		return p.pick(designSecondaryLayouts, count)
	case "white":
		return p.pick(designWhiteLayouts, count)
	case "three-view":
		return repeatedLayout(p.pick(designThreeViewLayouts, 1), count)
	case "sku":
		return repeatedLayout(p.pick(designSkuLayouts, 1), count)
	case "single":
		return p.pick(p.singlePool(in.Purpose), count)
	case "scene":
		return p.pick(designSceneLayouts, count)
	case "marketing", "poster", "social":
		return p.pick(designMarketingLayouts, count)
	case "amazon-a-plus":
		return p.pick(designAPlusLayouts, count)
	case "size":
		return p.pick(designSizeLayouts, count)
	case "material":
		return p.pick(designMaterialLayouts, count)
	case "package":
		return p.pick(designPackageLayouts, count)
	}
	return p.pick(p.mainPool(), count)
}

func (p *designPicker) mainPool() []designOption {
	if p.family == "listing" {
		return designListingMainLayouts
	}
	return designMainLayouts
}

func (p *designPicker) singlePool(purpose string) []designOption {
	switch strings.TrimSpace(purpose) {
	case "商品主图":
		return p.mainPool()
	case "商品副图":
		return designSecondaryLayouts
	case "Banner":
		return designBannerLayouts
	case "SKU 展示":
		return designSkuLayouts
	case "商品场景图":
		return designSceneLayouts
	case "自定义":
		return designCustomLayouts
	}
	return designSingleDefaultLayouts
}

// setLayouts fills the set roles in reading order; the scene roles show people
// when allowed, and listing main images stay compliant white.
func (p *designPicker) setLayouts(count int) []designOption {
	var roles []int
	if count <= len(designSetRoles) {
		roles = append(roles, designSetPriority[:count]...)
		slices.Sort(roles)
	} else {
		roles = []int{0, 1, 2, 3, 4, 5, 6}
		for i := 0; i < count-len(designSetRoles); i++ {
			roles = append(roles, designSetExtra[i%len(designSetExtra)])
		}
		roles = append(roles, len(designSetRoles)-1)
	}
	out := make([]designOption, 0, count)
	for _, index := range roles {
		role := designSetRoles[index]
		pool := role.variants
		if index == 0 && p.family == "listing" {
			pool = prefixedLayouts(role.name+"｜", designListingMainLayouts)
		}
		if role.people && p.people {
			if tagged := taggedLayouts(pool, "people"); len(tagged) > 0 {
				pool = tagged
			}
		}
		out = append(out, p.pick(pool, 1)...)
	}
	return out
}

// detailLayouts builds a detail set: an opener, shuffled middle roles with at
// least two close-ups and up to three people pages, and a product-information
// closer, ordered so adjacent pages never share an informationComponent. When
// people are allowed but the middle is too short for three people pages, the
// opener or closer shows people instead.
func (p *designPicker) detailLayouts(count int) []designOption {
	middleCount := max(count-2, 0)
	firstPool, lastPool := designDetailFirst, designDetailLast
	if short := 3 - min(3, middleCount-min(middleCount, 2)); p.people && count >= 4 && short > 0 {
		ends := []int{0, 1}
		p.shuffleInts(ends)
		for _, end := range ends[:min(short, 2)] {
			if end == 0 {
				firstPool = taggedLayouts(designDetailFirst, "people")
			} else {
				lastPool = taggedLayouts(designDetailLast, "people")
			}
		}
	}
	first, _ := p.pickOne(firstPool)
	if count == 1 {
		return []designOption{first}
	}
	last, _ := p.pickOne(lastPool)
	middle := p.orderMiddle(designComponent(first.text), p.detailMiddle(middleCount))
	return append(append([]designOption{first}, middle...), last)
}

func (p *designPicker) detailMiddle(n int) []designOption {
	if n <= 0 {
		return nil
	}
	type slot struct {
		role   int
		people bool
	}
	slots := make([]slot, 0, n)
	taken := map[int]bool{}
	add := func(role int, people bool) {
		slots = append(slots, slot{role: role, people: people})
		taken[role] = true
	}
	var closeups, peopleRoles []int
	for i, role := range designDetailRoles {
		if role.closeup {
			closeups = append(closeups, i)
		}
		if role.people {
			peopleRoles = append(peopleRoles, i)
		}
	}
	p.shuffleInts(closeups)
	p.shuffleInts(peopleRoles)
	for _, i := range closeups {
		if len(slots) < min(n, 2) {
			add(i, false)
		}
	}
	if p.people {
		want := min(3, n-len(slots))
		for _, i := range peopleRoles {
			if want <= 0 {
				break
			}
			add(i, true)
			want--
		}
	}
	for len(slots) < n {
		var rest []int
		for i := range designDetailRoles {
			if !taken[i] {
				rest = append(rest, i)
			}
		}
		if len(rest) == 0 {
			clear(taken)
			continue
		}
		p.shuffleInts(rest)
		for _, i := range rest {
			if len(slots) == n {
				break
			}
			add(i, false)
		}
	}
	out := make([]designOption, 0, n)
	for _, s := range slots {
		pool := designDetailRoles[s.role].variants
		if s.people {
			if tagged := taggedLayouts(pool, "people"); len(tagged) > 0 {
				pool = tagged
			}
		}
		out = append(out, p.pick(pool, 1)...)
	}
	return out
}

// orderMiddle shuffles the middle pages until no two adjacent pages (or the
// opener and the first middle page) share an informationComponent, and falls
// back to a most-frequent-first arrangement for long sets.
