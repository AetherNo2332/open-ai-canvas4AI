package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type EcommerceReference struct {
	ResourceID string `json:"resourceId"`
	Role       string `json:"role"`
	CatalogID  string `json:"catalogId,omitempty"`
	Name       string `json:"name,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type EcommerceSku struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	Spec       string `json:"spec"`
	Packaging  string `json:"packaging"`
	Claims     string `json:"claims"`
	Price      string `json:"price"`
	ResourceID string `json:"resourceId,omitempty"`
	Count      int    `json:"count"`
}

type EcommerceDraft struct {
	ProductName              string                    `json:"productName"`
	Category                 string                    `json:"category"`
	Brand                    string                    `json:"brand"`
	Description              string                    `json:"description"`
	Claims                   string                    `json:"claims"`
	Materials                string                    `json:"materials"`
	Colors                   string                    `json:"colors"`
	Specifications           string                    `json:"specifications"`
	Packaging                string                    `json:"packaging"`
	Dimensions               string                    `json:"dimensions"`
	Audience                 string                    `json:"audience"`
	Scene                    string                    `json:"scene"`
	PricePosition            string                    `json:"pricePosition"`
	LockedDetails            string                    `json:"lockedDetails"`
	Platform                 string                    `json:"platform"`
	Region                   string                    `json:"region"`
	ImageType                string                    `json:"imageType"`
	Count                    int                       `json:"count"`
	Language                 string                    `json:"language"`
	NoText                   bool                      `json:"noText"`
	AgentModel               string                    `json:"agentModel"`
	ImageModel               string                    `json:"imageModel"`
	Ratio                    string                    `json:"ratio"`
	Resolution               string                    `json:"resolution"`
	Style                    string                    `json:"style"`
	StylePrompt              string                    `json:"stylePrompt"`
	Fidelity                 string                    `json:"fidelity"`
	Instruction              string                    `json:"instruction"`
	SkillIDs                 []string                  `json:"skillIds,omitempty"`
	ModelEnabled             bool                      `json:"modelEnabled"`
	PrimaryModelResourceID   string                    `json:"primaryModelResourceId,omitempty"`
	PlanningTaskID           string                    `json:"planningTaskId,omitempty"`
	DirectionsTaskID         string                    `json:"directionsTaskId,omitempty"`
	CreativeRecommendationID string                    `json:"creativeRecommendationId,omitempty"`
	CreativeDirectionID      string                    `json:"creativeDirectionId,omitempty"`
	TypeOptions              map[string]string         `json:"typeOptions,omitempty"`
	CloneOptions             map[string]string         `json:"cloneOptions,omitempty"`
	CreativeOptions          *EcommerceCreativeOptions `json:"creativeOptions,omitempty"`
	ReferenceMode            string                    `json:"referenceMode,omitempty"`
	LayoutReferenceID        string                    `json:"layoutReferenceId,omitempty"`
	References               []EcommerceReference      `json:"references"`
	LayoutReferences         []EcommerceReference      `json:"layoutReferences"`
	Skus                     []EcommerceSku            `json:"skus"`
}

type SaveEcommerceProjectRequest struct {
	Revision int64                `json:"revision"`
	Name     string               `json:"name"`
	Draft    EcommerceDraft       `json:"draft"`
	Plan     *model.EcommercePlan `json:"plan,omitempty"`
	// Round-aware clients opt in explicitly. Legacy clients leave these empty
	// and keep the original whole-plan contract byte for byte.
	HistoryMode   string `json:"historyMode,omitempty"`
	AdvanceRound  bool   `json:"advanceRound,omitempty"`
	ActiveRoundID string `json:"activeRoundId,omitempty"`
}

type DeleteEcommerceProjectsRequest struct {
	IDs           []string `json:"ids"`
	All           bool     `json:"all"`
	ExpectedTotal *int64   `json:"expectedTotal"`
}

// EcommerceSkillPrompt reads only installed, active skills owned by this user.
func (s *Service) EcommerceSkillPrompt(userID string, ids []string) (string, error) {
	if len(ids) > 8 {
		return "", BadAuthRequest("每轮最多选择 8 个 Skills")
	}
	seen := make(map[string]bool, len(ids))
	parts := make([]string, 0, len(ids))
	total := 0
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return "", BadAuthRequest("技能 ID 不能为空或重复")
		}
		seen[id] = true
		skill, err := s.SkillDetail(userID, id)
		if err != nil {
			return "", err
		}
		if !skill.IsAdded || skill.Status != 1 {
			return "", BadAuthRequest("只能使用已安装且启用的技能")
		}
		file, err := s.SkillPackageFile(userID, id, "SKILL.md")
		if err != nil {
			return "", err
		}
		body, err := s.ecommerceSkillContext(userID, id, file)
		if err != nil {
			return "", err
		}
		latest, err := s.SkillDetail(userID, id)
		if err != nil {
			return "", err
		}
		if !latest.IsAdded || latest.Status != 1 || latest.VersionID != skill.VersionID || latest.ContentHash != skill.ContentHash {
			return "", creationConflict("技能在读取时已更新，请重新策划")
		}
		total += len(body)
		if total > 32000 {
			return "", BadAuthRequest("所选技能正文超过策划上下文上限，请减少选择")
		}
		parts = append(parts, fmt.Sprintf("技能 %q（版本 %s）：\n%s", skill.SkillName, skill.VersionID, body))
	}
	return strings.Join(parts, "\n\n"), nil
}

func (s *Service) CreateEcommerceProject(userID string) (*model.EcommerceProject, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	project := &model.EcommerceProject{ID: newID(), UserID: userID, Name: "未命名商品", Status: "draft", DraftJSON: "{}", PlanJSON: "{}", ActiveRoundID: newID(), Revision: 1}
	if err := s.repo.CreateEcommerceProject(project); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *Service) EcommerceProject(userID, id string) (*model.EcommerceProject, error) {
	return s.EcommerceProjectView(userID, id, false)
}

// EcommerceProjectView optionally returns only the bounded active round. This
// is a read-only projection: legacy inline history is never converted here.
func (s *Service) EcommerceProjectView(userID, id string, activeOnly bool) (*model.EcommerceProject, error) {
	project, err := s.repo.EcommerceProject(userID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, NotFound("电商项目不存在")
	}
	if err == nil {
		s.refreshEcommerceStatus(userID, project)
		var draft EcommerceDraft
		if json.Unmarshal([]byte(project.DraftJSON), &draft) == nil && draft.DirectionsTaskID == "" && draft.CreativeDirectionID == "" {
			if task, taskErr := s.repo.EcommerceDirectionsTask(userID, id, project.CreatedAt); taskErr == nil {
				var input struct {
					Metadata map[string]json.RawMessage `json:"metadata"`
				}
				var source EcommerceDraft
				if json.Unmarshal([]byte(task.InputJSON), &input) == nil {
					raw := input.Metadata["ecommerceDraft"]
					if json.Unmarshal(raw, &source) == nil {
						source.DirectionsTaskID, source.PlanningTaskID = "", ""
						current := draft
						current.DirectionsTaskID, current.PlanningTaskID = "", ""
						a, _ := json.Marshal(source)
						b, _ := json.Marshal(current)
						if string(a) == string(b) {
							draft.DirectionsTaskID = task.ID
							raw, _ = json.Marshal(draft)
							project.DraftJSON = string(raw)
						}
					}
				}
			}
		}
	}
	if err == nil {
		if activeOnly {
			project.PlanJSON = ecommerceActivePlanJSON(project.PlanJSON)
		} else if project.ActiveRoundID != "" {
			// Bounded compatibility view: clients that do not request the active
			// plan still see the newest rounds, never the full history.
			project.PlanJSON = s.ecommercePlanWithRecentRounds(userID, project)
		}
		if count, countErr := s.repo.EcommerceRoundCount(userID, project.ID); countErr == nil {
			project.RoundCount = count
		}
		s.fillEcommerceCoverTasks(userID, project)
	}
	return project, err
}

func ecommerceActivePlanJSON(planJSON string) string {
	var plan model.EcommercePlan
	if json.Unmarshal([]byte(planJSON), &plan) != nil || len(plan.PreviousImages) == 0 {
		return planJSON
	}
	plan.PreviousImages = nil
	raw, err := json.Marshal(plan)
	if err != nil {
		return planJSON
	}
	return string(raw)
}

// ecommercePlanWithRecentRounds keeps old clients working: they get the active
// plan plus a bounded page of newer history instead of the whole archive.
func (s *Service) ecommercePlanWithRecentRounds(userID string, project *model.EcommerceProject) string {
	var plan model.EcommercePlan
	if json.Unmarshal([]byte(project.PlanJSON), &plan) != nil {
		return project.PlanJSON
	}
	plan.PreviousImages = nil
	rounds, _, err := s.repo.EcommerceRoundPage(userID, project.ID, ecommerceRoundPageDefault, 0)
	if err != nil {
		return project.PlanJSON
	}
	previous := make([]model.EcommercePlanTask, 0)
	for i := len(rounds) - 1; i >= 0; i-- {
		var archived model.EcommercePlan
		if json.Unmarshal([]byte(rounds[i].PlanJSON), &archived) != nil {
			continue
		}
		for _, image := range archived.Images {
			image.RoundID = rounds[i].ID
			previous = append(previous, image)
		}
	}
	if len(previous) > 0 {
		plan.PreviousImages = previous
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return project.PlanJSON
	}
	return string(raw)
}

func (s *Service) fillEcommerceCoverTasks(userID string, project *model.EcommerceProject) {
	if project == nil {
		return
	}
	ids := make([]string, 0, 4)
	var plan model.EcommercePlan
	if json.Unmarshal([]byte(project.PlanJSON), &plan) == nil {
		for _, image := range plan.Images {
			if image.TaskID != "" {
				ids = append(ids, image.TaskID)
			}
		}
	}
	if len(ids) < 4 {
		if more, err := s.repo.EcommerceRoundCoverTaskIDs(userID, project.ID, 4-len(ids)); err == nil {
			ids = append(ids, more...)
		}
	}
	project.CoverTaskIDs = ids
}

func (s *Service) EcommerceProjects(userID string) ([]model.EcommerceProject, error) {
	projects, err := s.repo.EcommerceProjects(userID)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		s.refreshEcommerceStatus(userID, &projects[index])
		s.fillEcommerceListMeta(userID, &projects[index])
	}
	return projects, nil
}

func (s *Service) EcommerceProjectPage(userID string, page, pageSize int) ([]model.EcommerceProject, int64, error) {
	projects, total, err := s.repo.EcommerceProjectPage(userID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	for index := range projects {
		s.refreshEcommerceStatus(userID, &projects[index])
		s.fillEcommerceListMeta(userID, &projects[index])
	}
	return projects, total, nil
}

func (s *Service) fillEcommerceListMeta(userID string, project *model.EcommerceProject) {
	if count, err := s.repo.EcommerceRoundCount(userID, project.ID); err == nil {
		project.RoundCount = count
	}
	s.fillEcommerceCoverTasks(userID, project)
}

func (s *Service) DeleteEcommerceProjects(userID string, req DeleteEcommerceProjectsRequest) (int64, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return 0, err
	}
	if req.ExpectedTotal == nil || *req.ExpectedTotal < 1 || (req.All && len(req.IDs) != 0) || (!req.All && (len(req.IDs) == 0 || len(req.IDs) > 1000)) {
		return 0, BadAuthRequest("删除项目参数无效")
	}
	if !req.All {
		if *req.ExpectedTotal != int64(len(req.IDs)) {
			return 0, BadAuthRequest("删除项目数量无效")
		}
		seen := make(map[string]bool, len(req.IDs))
		for _, id := range req.IDs {
			if id == "" || len(id) > 64 || seen[id] {
				return 0, BadAuthRequest("项目 ID 为空、重复或过长")
			}
			seen[id] = true
		}
	}
	deleted, err := s.repo.DeleteEcommerceProjects(userID, req.IDs, req.All, *req.ExpectedTotal)
	if errors.Is(err, repository.ErrEcommerceProjectSelectionChanged) {
		return 0, creationConflict("项目列表已变化，请刷新后重新确认删除")
	}
	return deleted, err
}
func (s *Service) SaveEcommerceProject(userID, id string, req SaveEcommerceProjectRequest) (*model.EcommerceProject, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	project, err := s.EcommerceProject(userID, id)
	if err != nil {
		return nil, err
	}
	if req.Revision != project.Revision {
		return nil, BadAuthRequest("项目已在其他窗口更新，请刷新后重试")
	}
	if err := s.validateEcommerceDraftLimits(userID, &req); err != nil {
		return nil, err
	}
	if err := s.validateEcommerceDraftText(&req); err != nil {
		return nil, err
	}
	if err := s.validateEcommerceDraftDirection(userID, &req); err != nil {
		return nil, err
	}
	if err := s.validateEcommerceDraftImages(userID, &req); err != nil {
		return nil, err
	}
	if err := s.validateEcommerceDraftSkus(userID, &req); err != nil {
		return nil, err
	}
	if err := s.validateEcommerceDraftTasks(id, userID, &req); err != nil {
		return nil, err
	}
	if err := s.validateEcommercePlanHistory(userID, &req); err != nil {
		return nil, err
	}
	if err := s.validateEcommercePlannedDraft(userID, &req); err != nil {
		return nil, err
	}
	draftJSON, err := json.Marshal(req.Draft)
	if err != nil {
		return nil, err
	}
	planJSON := []byte("{}")
	if req.Plan != nil {
		planJSON, err = json.Marshal(req.Plan)
		if err != nil {
			return nil, err
		}
	}
	project.Name, err = ecommerceProjectName(req)
	if err != nil {
		return nil, err
	}
	project.DraftJSON = string(draftJSON)
	project.PlanJSON = string(planJSON)
	project.Status = "draft"
	if req.Draft.PlanningTaskID != "" {
		project.Status = "planning"
	} else if req.Plan != nil {
		project.Status = "awaiting_confirmation"
	}
	s.refreshEcommerceStatus(userID, project)
	return s.persistEcommerceProject(project, req)
}

func ecommerceProjectName(req SaveEcommerceProjectRequest) (string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.Draft.ProductName)
	}
	if name == "" {
		name = "未命名商品"
	}
	if len(name) > 160 {
		return "", BadAuthRequest("项目名称过长")
	}
	return name, nil
}

func (s *Service) refreshEcommerceStatus(userID string, project *model.EcommerceProject) {
	var draft EcommerceDraft
	if json.Unmarshal([]byte(project.DraftJSON), &draft) != nil {
		return
	}
	if s.refreshEcommerceDraftTaskStatus(userID, project, draft) {
		return
	}
	var plan model.EcommercePlan
	if json.Unmarshal([]byte(project.PlanJSON), &plan) != nil || len(plan.Images) == 0 {
		project.Status = "draft"
		return
	}
	queued, running, succeeded, failed, cancelled := 0, 0, 0, 0, 0
	for _, image := range plan.Images {
		if image.TaskID == "" {
			continue
		}
		task, err := s.repo.TaskForUser(userID, image.TaskID)
		if err != nil {
			continue
		}
		switch task.Status {
		case model.TaskStatusSucceeded:
			succeeded++
		case model.TaskStatusFailed:
			failed++
		case model.TaskStatusCancelled:
			cancelled++
		case model.TaskStatusRunning:
			running++
		default:
			queued++
		}
	}
	switch {
	case succeeded == len(plan.Images):
		project.Status = "completed"
	case running > 0:
		project.Status = "running"
	case queued > 0:
		project.Status = "queued"
	case succeeded > 0 && failed+cancelled > 0:
		project.Status = "partial_success"
	case failed > 0:
		project.Status = "failed"
	case cancelled > 0:
		project.Status = "cancelled"
	default:
		project.Status = "awaiting_confirmation"
	}
}

func validEcommerceReferenceRole(role string) bool {
	switch role {
	case "primary", "detail", "sku", "style", "logo", "package", "competitor", "model":
		return true
	}
	return false
}

func (s *Service) validateEcommercePlan(userID string, plan *model.EcommercePlan) error {
	if len(plan.InputSignature) > 128000 {
		return BadAuthRequest("策划参数快照过长")
	}
	if len(plan.Images) < 1 || len(plan.Images) > 30 {
		return BadAuthRequest("策划图片数量须为1到30张")
	}
	if strings.TrimSpace(plan.ProductSummary) == "" || strings.TrimSpace(plan.VisualStrategy) == "" {
		return BadAuthRequest("策划缺少商品分析或视觉策略")
	}
	seen := make(map[string]bool, len(plan.Images))
	seenTasks := make(map[string]bool, len(plan.Images))
	for index, image := range plan.Images {
		if image.ID == "" || seen[image.ID] {
			return BadAuthRequest(fmt.Sprintf("第%d张图片的策划ID无效", index+1))
		}
		seen[image.ID] = true
		for _, value := range []string{image.ImageType, image.Platform, image.Language, image.Goal, image.Theme, image.Composition, image.MainVisual, image.Fidelity, image.Prompt, image.ImageModel, image.Ratio, image.Resolution} {
			if value == "" {
				return BadAuthRequest(fmt.Sprintf("第%d张图片的策划字段不完整", index+1))
			}
		}
		if len(image.Prompt) > 32000 {
			return BadAuthRequest("图片提示词过长")
		}
		if image.TaskID == "" {
			if ecommerceagent.PlatformNoText(image.Platform, image.ImageType, "") && strings.TrimSpace(image.Copy) != "" {
				return BadAuthRequest(fmt.Sprintf("第%d张按所选平台采用无新增文字方案，请清空文案后再生成", index+1))
			}
			if phrase := ecommerceagent.RestrictedListingPhrase(image.Copy); phrase != "" {
				return BadAuthRequest(fmt.Sprintf("第%d张文案含高风险宣传词「%s」，请改为有依据的具体描述", index+1, phrase))
			}
		}
		if image.TaskID != "" {
			if seenTasks[image.TaskID] {
				return BadAuthRequest("同一生成任务不能绑定多张策划图")
			}
			seenTasks[image.TaskID] = true
			task, err := s.repo.TaskForUser(userID, image.TaskID)
			if err != nil || task.Type != "canvas_image" {
				return BadAuthRequest("生成任务不存在或不属于当前用户")
			}
		}
	}
	return nil
}

func (s *Service) refreshEcommerceDraftTaskStatus(userID string, project *model.EcommerceProject, draft EcommerceDraft) bool {
	if draft.PlanningTaskID != "" {
		if task, err := s.repo.TaskForUser(userID, draft.PlanningTaskID); err == nil {
			switch task.Status {
			case model.TaskStatusFailed:
				project.Status = "failed"
			case model.TaskStatusCancelled:
				project.Status = "cancelled"
			default:
				project.Status = "planning"
			}
		}
		return true
	}
	if draft.DirectionsTaskID != "" && draft.CreativeDirectionID == "" {
		if task, err := s.repo.TaskForUser(userID, draft.DirectionsTaskID); err == nil {
			switch task.Status {
			case model.TaskStatusFailed:
				project.Status = "failed"
			case model.TaskStatusCancelled:
				project.Status = "cancelled"
			case model.TaskStatusSucceeded:
				project.Status = "awaiting_confirmation"
			default:
				project.Status = "planning"
			}
			return true
		}
	}
	return false
}
