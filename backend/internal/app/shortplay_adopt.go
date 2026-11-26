package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W06 投送与收录。
//
// 投送：把作品资产绑到某一集，并在该集画布上投卡（已有卡只刷新）。
// 收录：自由画布上的一张局部卡升格成项目资产，再往目标集投一张新的域卡；
// 源卡原样不动，避免留下「有归属、位置错、永远不被刷新」的半同步卡。
// 画布单项生图回写：画布侧只知道 canvasId，归属与资源校验都在服务端。

// PlaceShortplayAssetCards 投送若干张资产卡进某一集。
func (s *Service) PlaceShortplayAssetCards(userID, ref, episodeID string, req shortplay.PlaceAssetRequest) (shortplay.PlaceAssetResult, error) {
	owner, err := s.shortplayRowForUser(userID, ref)
	if err != nil {
		return shortplay.PlaceAssetResult{}, err
	}
	episode, err := s.repo.ShortplayEpisodeByID(owner.ID, strings.TrimSpace(episodeID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.PlaceAssetResult{}, NewAppError(http.StatusNotFound, "目标集不存在")
	}
	if err != nil {
		return shortplay.PlaceAssetResult{}, err
	}
	if strings.TrimSpace(episode.CanvasID) == "" {
		return shortplay.PlaceAssetResult{}, BadAuthRequest("目标集还没有画布，先点「进精修画布」再投送")
	}
	assets, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return shortplay.PlaceAssetResult{}, err
	}
	byID := make(map[int]model.ShortplayAsset, len(assets))
	for _, row := range assets {
		byID[row.ID] = row
	}
	wanted := make([]int, 0, len(req.AssetIDs))
	seen := make(map[int]bool, len(req.AssetIDs))
	for _, id := range req.AssetIDs {
		if id <= 0 || seen[id] {
			continue
		}
		if _, ok := byID[id]; !ok {
			return shortplay.PlaceAssetResult{}, BadAuthRequest(fmt.Sprintf("资产 %d 不属于这部短剧", id))
		}
		seen[id] = true
		wanted = append(wanted, id)
	}
	if len(wanted) == 0 {
		return shortplay.PlaceAssetResult{}, BadAuthRequest("投送需要至少一个资产")
	}
	if err := s.shortplayBindAssetsToEpisode(owner, episode, wanted); err != nil {
		return shortplay.PlaceAssetResult{}, err
	}
	// 重读：绑定会把集号并进行里，回执与卡片快照都要用最新行。
	assets, err = s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return shortplay.PlaceAssetResult{}, err
	}
	snapshots := shortplayCardSnapshots(assets)
	cards := make([]shortplayCardSnapshot, 0, len(wanted))
	for _, id := range wanted {
		if snapshot, ok := snapshots[id]; ok {
			cards = append(cards, snapshot)
		}
	}
	nodeIDs, err := s.shortplayPlaceCardsOnCanvas(owner.UserID, episode.CanvasID, cards)
	if err != nil {
		return shortplay.PlaceAssetResult{}, err
	}
	numbers := shortplay.SplitEpisodeNumbersJSON(reReadEpisodeNumbers(assets, wanted))
	return shortplay.PlaceAssetResult{Placed: len(cards), NodeIDs: nodeIDs, EpisodeNumbers: numbers}, nil
}

// reReadEpisodeNumbers 取这次投送涉及资产的集号并集，供回执展示。
func reReadEpisodeNumbers(assets []model.ShortplayAsset, wanted []int) string {
	set := map[int]bool{}
	for _, row := range assets {
		for _, id := range wanted {
			if row.ID != id {
				continue
			}
			for _, number := range shortplay.SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON) {
				set[number] = true
			}
		}
	}
	numbers := make([]int, 0, len(set))
	for number := range set {
		numbers = append(numbers, number)
	}
	return encodeShortplayInts(numbers)
}

// shortplayBindAssetsToEpisode 把一批资产绑到一集：集行的资产列表一次写入，
// 资产行的集号数组各自取并集。集行必须先合并后写一次，逐个写会用旧快照互相覆盖。
func (s *Service) shortplayBindAssetsToEpisode(owner *model.Shortplay, episode *model.ShortplayEpisode, assetIDs []int) error {
	if err := s.repo.UpdateShortplayEpisodeAssetIDs(episode.ID, encodeShortplayInts(mergeShortplayInts(shortplayIntList(episode.AssetIDsJSON), assetIDs))); err != nil {
		return err
	}
	for _, assetID := range assetIDs {
		if err := s.shortplayBindAssetEpisodeNumber(owner, episode, assetID); err != nil {
			return err
		}
	}
	return nil
}

// shortplayBindAssetEpisodeNumber 把集号并进资产行的集号数组（CAS 重试一次）。
func (s *Service) shortplayBindAssetEpisodeNumber(owner *model.Shortplay, episode *model.ShortplayEpisode, assetID int) error {
	for attempt := 0; attempt < 2; attempt++ {
		row, err := s.repo.ShortplayAssetByID(owner.ID, assetID)
		if err != nil {
			return err
		}
		numbers := mergeShortplayInts(shortplay.SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON), []int{episode.Number})
		if len(numbers) == len(shortplay.SplitEpisodeNumbersJSON(row.EpisodeNumbersJSON)) {
			return nil
		}
		row.EpisodeNumbersJSON = encodeShortplayInts(numbers)
		applied, err := s.repo.UpdateShortplayAsset(row, row.Revision)
		if err != nil {
			return err
		}
		if applied {
			return nil
		}
	}
	return errors.New("资产集号绑定被并发修改，请重试")
}

// shortplayPlaceCardsOnCanvas 注入卡片并回读目标节点 ID。
func (s *Service) shortplayPlaceCardsOnCanvas(userID, canvasID string, cards []shortplayCardSnapshot) ([]string, error) {
	if err := s.injectShortplayCardsIntoCanvas(userID, canvasID, cards); err != nil {
		return nil, err
	}
	canvas, err := s.repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	wanted := make(map[int]bool, len(cards))
	for _, card := range cards {
		wanted[card.AssetID] = true
	}
	nodeIDs := make([]string, 0, len(cards))
	nodes, _ := doc["nodes"].([]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id := shortplayAssetCardID(node); wanted[id] {
			if nodeID, _ := node["id"].(string); nodeID != "" {
				nodeIDs = append(nodeIDs, nodeID)
				delete(wanted, id)
			}
		}
	}
	return nodeIDs, nil
}

// AdoptShortplayAsset 收录：局部卡 → 项目资产行 + 目标集上的一张新域卡。
func (s *Service) AdoptShortplayAsset(userID, ref string, req shortplay.AdoptAssetRequest) (shortplay.AdoptAssetResult, error) {
	owner, err := s.shortplayRowForUser(userID, ref)
	if err != nil {
		return shortplay.AdoptAssetResult{}, err
	}
	source, episode, err := s.shortplayAdoptInputs(userID, owner, req)
	if err != nil {
		return shortplay.AdoptAssetResult{}, err
	}
	assets, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return shortplay.AdoptAssetResult{}, err
	}
	row, created, err := s.adoptShortplayAssetRow(owner, assets, source)
	if err != nil {
		return shortplay.AdoptAssetResult{}, err
	}
	if err := s.shortplayBindAssetsToEpisode(owner, episode, []int{row.ID}); err != nil {
		return shortplay.AdoptAssetResult{}, err
	}
	card, adopted, err := s.shortplayAdoptCard(owner, row.ID, created, source)
	if err != nil {
		return shortplay.AdoptAssetResult{}, err
	}
	nodeIDs, err := s.shortplayPlaceCardsOnCanvas(owner.UserID, episode.CanvasID, []shortplayCardSnapshot{card})
	if err != nil {
		return shortplay.AdoptAssetResult{}, err
	}
	result := shortplay.AdoptAssetResult{
		AssetID: card.AssetID, NodeID: firstString(nodeIDs), Created: created, ImageAdopted: adopted,
		Revision: card.Revision, Kind: card.Kind, Name: card.Name, EpisodeNumbers: card.EpisodeNumbers,
	}
	if !created {
		result.Note = "同作品同分类下已有同名资产，已复用它并绑定到本集；原有设定与已有的图都保持不变"
	}
	return result, nil
}

// shortplayAdoptInputs 校验源画布/源卡/目标集，返回源卡投影与目标集。
func (s *Service) shortplayAdoptInputs(userID string, owner *model.Shortplay, req shortplay.AdoptAssetRequest) (shortplayAdoptSource, *model.ShortplayEpisode, error) {
	sourceCanvasID := strings.TrimSpace(req.CanvasID)
	nodeID := strings.TrimSpace(req.NodeID)
	episodeID := strings.TrimSpace(req.EpisodeID)
	if sourceCanvasID == "" || nodeID == "" || episodeID == "" {
		return shortplayAdoptSource{}, nil, BadAuthRequest("收录需要 canvasId（源画布）、nodeId（源卡）与 episodeId（目标集）")
	}
	canvas, err := s.repo.CanvasProjectForUser(userID, sourceCanvasID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplayAdoptSource{}, nil, NewAppError(http.StatusNotFound, "源画布不存在")
	}
	if err != nil {
		return shortplayAdoptSource{}, nil, err
	}
	// 源画布不能已经是集画布：卡本来就有归属，跨集移动走复制粘贴。
	if _, linked, err := s.ShortplayByCanvas(userID, sourceCanvasID); err != nil {
		return shortplayAdoptSource{}, nil, err
	} else if linked {
		return shortplayAdoptSource{}, nil, BadAuthRequest("这张卡已经在集画布上，直接复制粘贴到目标集即可，不需要收录")
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return shortplayAdoptSource{}, nil, BadAuthRequest("源画布内容无法解析，无法收录")
	}
	node, ok := shortplayDocumentNode(doc, nodeID)
	if !ok {
		return shortplayAdoptSource{}, nil, NewAppError(http.StatusNotFound, "源画布上找不到这张卡")
	}
	source, err := shortplayAdoptSourceOf(node)
	if err != nil {
		return shortplayAdoptSource{}, nil, err
	}
	episode, err := s.repo.ShortplayEpisodeByID(owner.ID, episodeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplayAdoptSource{}, nil, NewAppError(http.StatusNotFound, "目标集不存在")
	}
	if err != nil {
		return shortplayAdoptSource{}, nil, err
	}
	if strings.TrimSpace(episode.CanvasID) == "" {
		return shortplayAdoptSource{}, nil, BadAuthRequest("目标集还没有画布，无法投射资产卡")
	}
	return source, episode, nil
}

// shortplayAdoptCard 把源卡上的图带到目标资产行（新建行直接写，复用行只在无图时补），
// 再返回含最新 revision 与集号的卡片快照。
func (s *Service) shortplayAdoptCard(owner *model.Shortplay, assetID int, created bool, source shortplayAdoptSource) (shortplayCardSnapshot, bool, error) {
	adopted := false
	if source.ImageResourceID != "" {
		row, err := s.repo.ShortplayAssetByID(owner.ID, assetID)
		if err != nil {
			return shortplayCardSnapshot{}, false, err
		}
		// 已有图不覆盖：那是别的集正在用的一张图。
		if created || strings.TrimSpace(row.ImageResourceID) == "" {
			applied, writeErr := s.repo.SetShortplayAssetImageFrom(owner.ID, assetID, shortplayAssetAdoptImage(source), shortplay.ImageAnyState())
			if writeErr != nil {
				return shortplayCardSnapshot{}, false, writeErr
			}
			adopted = applied
		}
	}
	fresh, err := s.repo.ShortplayAssets(owner.ID)
	if err != nil {
		return shortplayCardSnapshot{}, false, err
	}
	card, ok := shortplayCardSnapshots(fresh)[assetID]
	if !ok {
		return shortplayCardSnapshot{}, false, errors.New("收录后的资产行读不回来，请重试")
	}
	card.EpisodeNumbers = shortplay.SplitEpisodeNumbersJSON(reReadEpisodeNumbers(fresh, []int{assetID}))
	return card, adopted, nil
}
func shortplayAssetAdoptImage(source shortplayAdoptSource) repository.ShortplayAssetImageWrite {
	// 收录把源卡上的图带过来，提示词记成「收录」以便追溯来源。
	return repository.ShortplayAssetImageWrite{ResourceID: source.ImageResourceID, Prompt: "收录自画布局部卡"}
}

// shortplayAdoptSource 是源卡的字段投影；只从节点快照取，不接受请求体覆盖。
type shortplayAdoptSource struct {
	Kind            string
	Name            string
	Description     string
	Appearance      string
	Aliases         []string
	Voice           string
	Locked          bool
	ImageResourceID string
}

func shortplayDocumentNode(doc map[string]any, nodeID string) (map[string]any, bool) {
	nodes, ok := doc["nodes"].([]any)
	if !ok {
		return nil, false
	}
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := node["id"].(string); id == nodeID {
			return node, true
		}
	}
	return nil, false
}

// shortplayAdoptSourceOf 读节点上的局部卡字段；不是资产卡或没有分类时给可行动的拒绝。
func shortplayAdoptSourceOf(node map[string]any) (shortplayAdoptSource, error) {
	metadata, _ := node["metadata"].(map[string]any)
	if metadata == nil || stringValue(node["type"]) != shortplayAssetNodeType {
		return shortplayAdoptSource{}, BadAuthRequest("请先选中一张资产卡（角色 / 道具 / 场景卡），只有它们能收录为项目资产")
	}
	if existing := shortplayMetadataAssetID(metadata["shortplayAssetId"]); existing > 0 {
		return shortplayAdoptSource{}, BadAuthRequest(fmt.Sprintf("这张卡已经是项目资产（%d），不需要收录", existing))
	}
	kind := strings.TrimSpace(stringValue(metadata["shortplayAssetCategory"]))
	if kind == "" {
		return shortplayAdoptSource{}, BadAuthRequest("这张卡还没有分类：先在卡上选「角色 / 道具 / 场景」，再收录")
	}
	if err := shortplay.ValidateAssetKind(kind); err != nil {
		return shortplayAdoptSource{}, BadAuthRequest(err.Error())
	}
	name := strings.TrimSpace(stringValue(node["title"]))
	if name == "" {
		return shortplayAdoptSource{}, BadAuthRequest("这张卡没有名称：先给卡起个名字，再收录")
	}
	if len([]rune(name)) > 120 {
		return shortplayAdoptSource{}, BadAuthRequest("资产名称不能超过 120 字")
	}
	return shortplayAdoptSource{
		Kind:            kind,
		Name:            name,
		Description:     truncateShortplayText(stringValue(metadata["content"]), 2000),
		Appearance:      truncateShortplayText(stringValue(metadata["shortplayAssetAppearance"]), 2000),
		Aliases:         shortplayMetadataStringList(metadata["shortplayAssetAliases"]),
		Voice:           strings.TrimSpace(stringValue(metadata["shortplayAssetVoice"])),
		Locked:          metadata["shortplayAssetLocked"] == true,
		ImageResourceID: strings.TrimSpace(stringValue(metadata["shortplayAssetImageResourceId"])),
	}, nil
}

// adoptShortplayAssetRow 建行或复用同分类同名行；复用时只绑集，不改设定、不换图。
func (s *Service) adoptShortplayAssetRow(owner *model.Shortplay, assets []model.ShortplayAsset, source shortplayAdoptSource) (*model.ShortplayAsset, bool, error) {
	name := strings.TrimSpace(source.Name)
	if name == "" {
		return nil, false, BadAuthRequest("资产名称不能为空")
	}
	for index := range assets {
		if assets[index].Kind == source.Kind && strings.TrimSpace(assets[index].Name) == name && assets[index].DerivedFrom == 0 {
			return &assets[index], false, nil
		}
	}
	now := time.Now()
	for attempt := 0; attempt < 2; attempt++ {
		row, buildErr := shortplay.NewBaseAssetRow(owner.ID, shortplay.NextAssetID(assets), shortplay.UpsertAssetRequest{
			Kind: source.Kind, Name: name, Aliases: source.Aliases, Description: source.Description,
			Appearance: source.Appearance, Voice: source.Voice, Locked: source.Locked,
		})
		if buildErr != nil {
			return nil, false, BadAuthRequest(buildErr.Error())
		}
		row.Revision = 1
		row.EpisodeNumbersJSON = "[]"
		row.Position = nextShortplayAssetPosition(assets)
		row.CreatedAt, row.UpdatedAt = now, now
		if err := s.repo.CreateShortplayAsset(&row); err == nil {
			return &row, true, nil
		} else if attempt == 0 && isShortplayUniqueViolation(err) {
			if assets, err = s.repo.ShortplayAssets(owner.ID); err != nil {
				return nil, false, err
			}
			continue
		}
	}
	return nil, false, errors.New("资产序号被并发占用，请重试收录")
}

// ApplyShortplayAssetImageFromCanvas 画布侧单项生图回写：按画布反查集归属后写资产表。
func (s *Service) ApplyShortplayAssetImageFromCanvas(userID, canvasID string, assetID int, resourceID string) (shortplay.AssetSummary, bool, error) {
	binding, linked, err := s.ShortplayByCanvas(userID, canvasID)
	if err != nil {
		return shortplay.AssetSummary{}, false, err
	}
	if !linked {
		return shortplay.AssetSummary{}, false, BadAuthRequest("这张画布不是短剧分集画布，生成结果不写资产表")
	}
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return shortplay.AssetSummary{}, false, BadAuthRequest("回写需要 resourceId")
	}
	resource, err := s.repo.ResourceForUser(userID, resourceID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return shortplay.AssetSummary{}, false, BadAuthRequest("资源不存在或不属于当前账号")
	}
	if err != nil {
		return shortplay.AssetSummary{}, false, err
	}
	if resource.Kind != "image" || !strings.HasPrefix(resource.MimeType, "image/") || resource.Status != model.ResourceStatusReady {
		return shortplay.AssetSummary{}, false, BadAuthRequest("回写只接受已就绪的图片资源")
	}
	owner, err := s.repo.ShortplayByID(binding.ShortplayID)
	if err != nil {
		return shortplay.AssetSummary{}, false, err
	}
	if owner.UserID != userID {
		return shortplay.AssetSummary{}, false, NewAppError(http.StatusNotFound, "短剧不存在")
	}
	applied, err := s.repo.SetShortplayAssetImageFrom(binding.ShortplayID, assetID, repository.ShortplayAssetImageWrite{
		ResourceID: resourceID, Prompt: "画布单项生成",
	}, shortplay.ImageAnyState())
	if err != nil {
		return shortplay.AssetSummary{}, false, err
	}
	assets, err := s.repo.ShortplayAssets(binding.ShortplayID)
	if err != nil {
		return shortplay.AssetSummary{}, false, err
	}
	byID := make(map[int]model.ShortplayAsset, len(assets))
	for _, row := range assets {
		byID[row.ID] = row
	}
	row, ok := byID[assetID]
	if !ok {
		return shortplay.AssetSummary{}, false, NewAppError(http.StatusNotFound, "资产不属于这部短剧")
	}
	resolved := row
	if row.DerivedFrom != 0 {
		if parent, exists := byID[row.DerivedFrom]; exists {
			resolved = shortplay.ResolveAsset(&parent, &row)
		}
	}
	return shortplay.BuildAssetSummary(resolved), applied, nil
}

func shortplayMetadataStringList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func truncateShortplayText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
