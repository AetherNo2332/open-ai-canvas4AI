package app

import (
	"context"
	"encoding/json"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// createCloudAgentRunWithinStorageQuota 是"建 run"的存储配额边界。占位任务、报价预留与
// 执行记录必须在同一事务里提交，所以配额校验与写入共用同一个 storageMu 临界区。
func (s *Service) createCloudAgentRunWithinStorageQuota(run *model.CloudAgentExecution, task *model.Task, billingOrder *model.BillingOrder, policy RuntimePolicySetting, skillRows ...[]model.AgentConversationSkill) error {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	usage, err := s.repo.UserStorageUsage(task.UserID)
	if err != nil {
		return err
	}
	incomingBytes := int64(len([]byte(task.Prompt)) + len([]byte(task.InputJSON)) + len([]byte(task.Error)))
	if err := validateTaskStorageQuotaWithPolicy(usage, incomingBytes, policy.Resource); err != nil {
		return err
	}
	return s.repo.CreateCloudAgentHoldingTask(task, billingOrder, run, policy.Task.ActiveTaskLimit, skillRows...)
}

// structuredWriteQuota 是 Agent 画布命令与媒体版本写入的结构化数据额度；策略与用量都从
// 已加锁的事务里读取，不走根连接池。
func (s *Service) structuredWriteQuota(tx *repository.Repository, ownerID, kind string, countDelta, deltaBytes int64) error {
	reader := &Service{repo: tx, dataDir: s.dataDir}
	policy, err := reader.RuntimePolicy()
	if err != nil {
		return err
	}
	usage, err := tx.UserStructuredStorageUsage(ownerID)
	if err != nil {
		return err
	}
	if countDelta <= 0 {
		return validateStructuredStorageQuotaWithPolicy(usage, kind, false, deltaBytes, policy.Resource)
	}
	count, bytes := usage.CanvasCount+countDelta, usage.CanvasBytes+deltaBytes
	if kind == "asset" {
		count, bytes = usage.AssetCount+countDelta, usage.AssetBytes+deltaBytes
	}
	return validateStructuredReplacementQuotaWithPolicy(usage, kind, int(count), bytes, policy.Resource)
}

// CanvasCommandValidation 汇总当前可用媒体模型的参考素材上限，供画布命令校验 Agent 写入的生成配置。
func (s *Service) CanvasCommandValidation(ctx context.Context, tx *repository.Repository, p identity.Principal) (commands.ValidationContext, error) {
	v := commands.ValidationContext{Principal: p, ModelInputLimits: map[string]map[string]int{}}
	catalog, err := s.ModelCatalog(nil)
	if err != nil {
		return v, err
	}
	for _, channel := range catalog.Channels {
		for _, m := range channel.Models {
			mode := normalizeCapability(m.Capability)
			if !m.Available || (mode != "image" && mode != "video") {
				continue
			}
			raw, err := json.Marshal(m.CapabilityConfig)
			if err != nil {
				return v, err
			}
			var profile ModelCapabilityConfig
			if err = json.Unmarshal(raw, &profile); err != nil {
				return v, err
			}
			limits := map[string]int{}
			if mode == "image" && profile.Image != nil {
				limits["image"] = profile.Image.References.MaxImages
			}
			if mode == "video" && profile.Video != nil {
				limits["image"], limits["video"], limits["audio"] = profile.Video.References.MaxImages, profile.Video.References.MaxVideos, profile.Video.References.MaxAudios
			}
			if v.ModelInputLimits[mode] == nil {
				v.ModelInputLimits[mode] = map[string]int{"image": 0, "video": 0, "audio": 0}
			}
			for kind, count := range limits {
				if count > v.ModelInputLimits[mode][kind] {
					v.ModelInputLimits[mode][kind] = count
				}
			}
		}
	}
	return v, nil
}
