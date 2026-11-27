package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W06 资产卡投送：把某集的资产行渲染成集画布上的 `shortplay-asset` 节点。
//
// 卡片是资产表的只读投影：节点自己带封面、状态、音色与 revision 快照，渲染零表依赖。
// 三条性质：
//   - 幂等：已有同 shortplayAssetId 的卡只刷新快照、不动位置；无差异时整张画布不写
//     （不递增 revision，重跑提取不会因为布局已正确而制造写冲突）。
//   - 与画布写入合同一致：只改顶层 nodes，其余字段原样保留；落库统一走 UpsertCanvasProject
//     并带读到的 revision 做 CAS。
//   - 画布缺失或 payload 损坏时跳过该集而不是中断：资产数据比一张派生投影重要。

const (
	shortplayAssetNodeType    = "shortplay-asset"
	shortplayAssetCardWidth   = 264
	shortplayAssetCardHeight  = 352
	shortplayAssetCardGapX    = 48
	shortplayAssetCardGapY    = 48
	shortplayAssetCardColumns = 4
	// 卡片区从 story_input 右侧再留一段，给两区之间的连线留位置。
	shortplayAssetCardStoryGap = 120
)

// shortplayCardSnapshot 是注入一张卡所需的全部字段，只在服务端组装。
type shortplayCardSnapshot struct {
	AssetID        int
	Name           string
	Content        string
	Appearance     string
	Aliases        []string
	Voice          string
	Locked         bool
	Kind           string
	DerivedFrom    int
	VariantName    string
	RenderRule     string
	BaseImageStale bool
	ImageResource  string
	ImageVersion   int
	CoverURL       string
	ImageStatus    string
	AudioResource  string
	AudioURL       string
	AudioStatus    string
	AudioVersion   int
	BaseAudioStale bool
	Position       int
	// EpisodeNumbers 只在收录回执里使用，不落节点。
	EpisodeNumbers []int
	// Revision 是快照对应的表行版本，落成节点的 shortplayAssetRevision：回写 CAS 的令牌。
	Revision int64
}

// shortplayCardSnapshots 把资产行摊成快照；衍生行按父资产合成显示名与继承字段。
func shortplayCardSnapshots(assets []model.ShortplayAsset) map[int]shortplayCardSnapshot {
	byID := make(map[int]model.ShortplayAsset, len(assets))
	for _, row := range assets {
		byID[row.ID] = row
	}
	snapshots := make(map[int]shortplayCardSnapshot, len(assets))
	for _, row := range assets {
		resolved := row
		if row.DerivedFrom != 0 {
			if parent, ok := byID[row.DerivedFrom]; ok {
				resolved = shortplay.ResolveAsset(&parent, &row)
			}
		}
		status, cover := shortplayAssetMedia(row.ImageResourceID, row.ImageStatus)
		audioStatus, audioURL := shortplayAssetMedia(row.AudioResourceID, row.AudioStatus)
		snapshots[row.ID] = shortplayCardSnapshot{
			AssetID:        row.ID,
			Name:           resolved.Name,
			Content:        resolved.Description,
			Appearance:     resolved.Appearance,
			Aliases:        shortplay.SplitAliasesJSON(resolved.AliasesJSON),
			Voice:          resolved.Voice,
			Locked:         row.Locked,
			Kind:           resolved.Kind,
			DerivedFrom:    row.DerivedFrom,
			VariantName:    row.VariantName,
			RenderRule:     firstNonEmpty(resolved.RenderRule, shortplay.DefaultRenderRule),
			BaseImageStale: row.BaseImageStale,
			ImageResource:  row.ImageResourceID,
			ImageVersion:   row.ImageVersion,
			CoverURL:       cover,
			ImageStatus:    status,
			AudioResource:  row.AudioResourceID,
			AudioURL:       audioURL,
			AudioStatus:    audioStatus,
			AudioVersion:   row.AudioVersion,
			BaseAudioStale: row.BaseAudioStale,
			Position:       row.Position,
			Revision:       row.Revision,
		}
	}
	return snapshots
}

// shortplayAssetMedia 归一「资源 + 状态」：有资源才算 ready；没资源只有在途状态值得透出。
func shortplayAssetMedia(resourceID, status string) (string, string) {
	resourceID = strings.TrimSpace(resourceID)
	status = strings.TrimSpace(status)
	if resourceID == "" {
		if status == shortplay.ImageQueued || status == shortplay.ImageRunning {
			return status, ""
		}
		return "", ""
	}
	if status == "" {
		status = shortplay.ImageReady
	}
	return status, resourceFileURL(resourceID)
}

// injectShortplayAssetCards 把该作品各集已绑定的资产投成卡；幂等，失败只记录。
// 提取落库后调用；失败不阻断提取结论，下一次提取或手动投送会再对齐。
func (s *Service) injectShortplayAssetCards(shortplayID string) {
	owner, err := s.repo.ShortplayByID(shortplayID)
	if err != nil {
		return
	}
	episodes, err := s.repo.ShortplayEpisodes(shortplayID)
	if err != nil {
		slog.Warn("shortplay asset card inject skipped: episodes unreadable", "shortplay", shortplayID, "error", err)
		return
	}
	assets, err := s.repo.ShortplayAssets(shortplayID)
	if err != nil {
		slog.Warn("shortplay asset card inject skipped: assets unreadable", "shortplay", shortplayID, "error", err)
		return
	}
	snapshots := shortplayCardSnapshots(assets)
	for _, episode := range episodes {
		ids := shortplayIntList(episode.AssetIDsJSON)
		if len(ids) == 0 || strings.TrimSpace(episode.CanvasID) == "" {
			continue
		}
		cards := make([]shortplayCardSnapshot, 0, len(ids))
		for _, id := range ids {
			if snapshot, ok := snapshots[id]; ok {
				cards = append(cards, snapshot)
			}
		}
		if err := s.injectShortplayCardsIntoCanvas(owner.UserID, episode.CanvasID, cards); err != nil {
			slog.Warn("shortplay asset card inject skipped", "canvas", episode.CanvasID, "error", err)
		}
	}
}

// injectShortplayCardsIntoCanvas 把一批卡并进一张画布；无变化时不写库。
func (s *Service) injectShortplayCardsIntoCanvas(userID, canvasID string, cards []shortplayCardSnapshot) error {
	if len(cards) == 0 {
		return nil
	}
	canvas, err := s.repo.CanvasProjectForUser(userID, canvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return err
	}
	changed, err := shortplayInjectCardsIntoDocument(doc, cards, time.Now())
	if err != nil || !changed {
		return err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	canvas.PayloadJSON, canvas.UpdatedAt = string(raw), time.Now()
	return s.repo.UpsertCanvasProject(canvas)
}

// shortplayInjectCardsIntoDocument 把卡片并进画布文档，返回是否有实际改动。
// 只改 nodes 一个键：connections / viewport / 前端私有字段原样保留。
func shortplayInjectCardsIntoDocument(doc map[string]any, cards []shortplayCardSnapshot, now time.Time) (bool, error) {
	if len(cards) == 0 {
		return false, nil
	}
	rawNodes, ok := doc["nodes"]
	if !ok || rawNodes == nil {
		rawNodes = []any{}
	}
	nodes, ok := rawNodes.([]any)
	if !ok {
		return false, errors.New("画布 nodes 不是数组")
	}
	ordered := make([]shortplayCardSnapshot, len(cards))
	copy(ordered, cards)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if rank := shortplayCardKindRank(left.Kind) - shortplayCardKindRank(right.Kind); rank != 0 {
			return rank < 0
		}
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		return left.Name < right.Name
	})

	byAsset := make(map[int]map[string]any, len(nodes))
	occupied := make([]shortplayRect, 0, len(nodes))
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id := shortplayAssetCardID(node); id > 0 {
			byAsset[id] = node
		}
		x, y := shortplayNodePosition(node)
		width, height := shortplayNodeSize(node)
		occupied = append(occupied, shortplayRect{x: x, y: y, w: width, h: height})
	}
	originX, originY := shortplayAssetCardOrigin(nodes)

	changed := false
	row, col, lastKind := 0, 0, ""
	for _, card := range ordered {
		if card.Kind != lastKind {
			if lastKind != "" {
				row++
			}
			col, lastKind = 0, card.Kind
		}
		if node, exists := byAsset[card.AssetID]; exists {
			// 已有卡只刷新内容，不动位置：用户拖过的地方不能被重跑提取重置。
			if shortplayRefreshAssetCard(node, card) {
				changed = true
			}
		} else {
			x, y := shortplayPlaceAssetCard(occupied, originX, originY, col, row)
			node := shortplayAssetCardNode(card, x, y, now)
			nodes = append(nodes, node)
			byAsset[card.AssetID] = node
			occupied = append(occupied, shortplayRect{x: x, y: y, w: shortplayAssetCardWidth, h: shortplayAssetCardHeight})
			changed = true
		}
		col++
		if col >= shortplayAssetCardColumns {
			col, row = 0, row+1
		}
	}
	if !changed {
		return false, nil
	}
	doc["nodes"] = nodes
	return true, nil
}

// shortplayRefreshAssetCard 用表快照刷新已有卡；返回是否有字段变化。
func shortplayRefreshAssetCard(node map[string]any, card shortplayCardSnapshot) bool {
	before, err := json.Marshal(node)
	if err != nil {
		return false
	}
	metadata, ok := node["metadata"].(map[string]any)
	if !ok {
		metadata = map[string]any{}
		node["metadata"] = metadata
	}
	node["title"] = card.Name
	shortplayApplyCardMetadata(metadata, card)
	after, err := json.Marshal(node)
	if err != nil {
		return true
	}
	return string(before) != string(after)
}

// shortplayApplyCardMetadata 写入卡片快照字段；注入与全量刷新共用一份字段表。
func shortplayApplyCardMetadata(metadata map[string]any, card shortplayCardSnapshot) {
	delete(metadata, "workflowKind")
	metadata["content"] = card.Content
	metadata["shortplayAssetId"] = card.AssetID
	metadata["shortplayAssetAliases"] = shortplayCardAliases(card.Aliases)
	metadata["shortplayAssetVoice"] = card.Voice
	metadata["shortplayAssetLocked"] = card.Locked
	metadata["shortplayAssetCategory"] = card.Kind
	metadata["shortplayAssetAppearance"] = card.Appearance
	metadata["shortplayAssetImageResourceId"] = card.ImageResource
	metadata["storageKey"] = shortplayCardStorageKey(card.ImageResource)
	metadata["shortplayAssetImageVersion"] = card.ImageVersion
	metadata["shortplayAssetCoverUrl"] = card.CoverURL
	metadata["shortplayAssetImageStatus"] = card.ImageStatus
	metadata["shortplayAssetDerivedFrom"] = card.DerivedFrom
	metadata["shortplayAssetVariantName"] = card.VariantName
	metadata["shortplayAssetRenderRule"] = card.RenderRule
	metadata["shortplayAssetBaseImageStale"] = card.BaseImageStale
	metadata["shortplayAssetAudioResourceId"] = card.AudioResource
	metadata["shortplayAssetAudioUrl"] = card.AudioURL
	metadata["shortplayAssetAudioStatus"] = card.AudioStatus
	metadata["shortplayAssetAudioVersion"] = card.AudioVersion
	metadata["shortplayAssetBaseAudioStale"] = card.BaseAudioStale
	metadata["shortplayAssetRevision"] = card.Revision
	metadata["status"] = "success"
	metadata["fontSize"] = 14
}

// shortplayAssetCardNode 构造一张卡；字段名与前端 canvas-shortplay-asset.ts 逐字段对齐。
func shortplayAssetCardNode(card shortplayCardSnapshot, x, y int, now time.Time) map[string]any {
	stamp := now.UTC().Format("2006-01-02T15:04:05.000Z")
	metadata := map[string]any{}
	shortplayApplyCardMetadata(metadata, card)
	return map[string]any{
		"id":        newID(),
		"type":      shortplayAssetNodeType,
		"title":     card.Name,
		"createdAt": stamp,
		"updatedAt": stamp,
		"position":  map[string]any{"x": float64(x), "y": float64(y)},
		"width":     float64(shortplayAssetCardWidth),
		"height":    float64(shortplayAssetCardHeight),
		"metadata":  metadata,
	}
}

func shortplayCardAliases(aliases []string) []string {
	if aliases == nil {
		return []string{}
	}
	return aliases
}

func shortplayCardStorageKey(resourceID string) string {
	if strings.TrimSpace(resourceID) == "" {
		return ""
	}
	return "resource:" + resourceID
}

func shortplayCardKindRank(kind string) int {
	switch kind {
	case shortplay.AssetCharacter:
		return 0
	case shortplay.AssetProp:
		return 1
	case shortplay.AssetScene:
		return 2
	}
	return 3
}

// shortplayAssetCardID 读节点上的资产行 ID；0 即局部卡，不参与刷新。
func shortplayAssetCardID(node map[string]any) int {
	metadata, _ := node["metadata"].(map[string]any)
	if metadata == nil {
		return 0
	}
	return shortplayMetadataAssetID(metadata["shortplayAssetId"])
}

func shortplayMetadataAssetID(value any) int {
	switch item := value.(type) {
	case float64:
		if item > 0 && item == float64(int(item)) {
			return int(item)
		}
	case int:
		if item > 0 {
			return item
		}
	case int64:
		if item > 0 {
			return int(item)
		}
	case json.Number:
		if id, err := item.Int64(); err == nil && id > 0 {
			return int(id)
		}
	}
	return 0
}

// shortplayAssetCardOrigin 取卡片区左上角：story_input 右侧；没有它就从画布原点开始。
func shortplayAssetCardOrigin(nodes []any) (int, int) {
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if metadata, _ := node["metadata"].(map[string]any); metadata["workflowKind"] != "story_input" {
			continue
		}
		x, y := shortplayNodePosition(node)
		width, _ := shortplayNodeSize(node)
		return x + width + shortplayAssetCardStoryGap, y
	}
	return 0, 0
}

func shortplayNodePosition(node map[string]any) (int, int) {
	position, _ := node["position"].(map[string]any)
	return shortplayNodeNumber(position["x"]), shortplayNodeNumber(position["y"])
}

func shortplayNodeSize(node map[string]any) (int, int) {
	width := shortplayNodeNumber(node["width"])
	height := shortplayNodeNumber(node["height"])
	if width <= 0 {
		width = 240
	}
	if height <= 0 {
		height = 160
	}
	return width, height
}

func shortplayNodeNumber(value any) int {
	switch item := value.(type) {
	case float64:
		return int(item)
	case int:
		return item
	case int64:
		return int(item)
	case json.Number:
		if number, err := item.Int64(); err == nil {
			return int(number)
		}
	}
	return 0
}

// shortplayRect 是画布上的已占矩形；卡片落位按矩形做线性探测。
type shortplayRect struct{ x, y, w, h int }

// shortplayPlaceAssetCard 从期望槽位线性探测空位：先向右，排满一行再下移一行。
func shortplayPlaceAssetCard(occupied []shortplayRect, originX, originY, col, row int) (int, int) {
	for attempt := 0; attempt < 512; attempt++ {
		x := originX + col*(shortplayAssetCardWidth+shortplayAssetCardGapX)
		y := originY + row*(shortplayAssetCardHeight+shortplayAssetCardGapY)
		candidate := shortplayRect{x: x, y: y, w: shortplayAssetCardWidth, h: shortplayAssetCardHeight}
		if shortplayRectFree(occupied, candidate) {
			return x, y
		}
		col++
		if col >= shortplayAssetCardColumns {
			col, row = 0, row+1
		}
	}
	return originX, originY
}

// shortplayRectFree 判定候选卡位是否空闲：按半个间距外扩后再比，避免卡片贴边。
func shortplayRectFree(occupied []shortplayRect, candidate shortplayRect) bool {
	for _, rect := range occupied {
		if candidate.x < rect.x+rect.w+shortplayAssetCardGapX/2 &&
			candidate.x+candidate.w+shortplayAssetCardGapX/2 > rect.x &&
			candidate.y < rect.y+rect.h+shortplayAssetCardGapY/2 &&
			candidate.y+candidate.h+shortplayAssetCardGapY/2 > rect.y {
			return false
		}
	}
	return true
}
