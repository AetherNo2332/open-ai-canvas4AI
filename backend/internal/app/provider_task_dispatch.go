package app

import (
	"context"
	"errors"
	"strings"

	"yingce/backend/internal/model"
)

func (s *Service) processWorkflowGenerationTask(ctx context.Context, userID string, input canvasGenerationInput) (map[string]interface{}, error) {
	if err := s.RequireWorkflowPluginForInterface(input.Config.InterfaceType); err != nil {
		return nil, err
	}
	if err := validateWorkflowProviderPromptLength(input); err != nil {
		return nil, err
	}
	if err := validateWorkflowProviderConfig(input.Mode, input.Config); err != nil {
		return nil, err
	}
	// 工作流参数由工作流字段定义校验，普通模型能力配置不能覆盖它们。
	if resumedProviderRequestID(ctx) == "" {
		if err := s.hydrateGenerationMedia(userID, &input, providerMediaHydrationPolicy{}); err != nil {
			return nil, err
		}
	}
	return s.runWorkflowProviderTask(ctx, input)
}

func (s *Service) processTextGenerationTask(ctx context.Context, userID string, input canvasGenerationInput, promptTemplateOperation string) (map[string]interface{}, error) {
	if input.AgentRequests != nil {
		resolved, err := resolveAgentResourcePlaceholders(input, true)
		if err != nil {
			return nil, err
		}
		return runAgentToolTask(ctx, resolved)
	}
	var result map[string]interface{}
	var err error
	if draft, ok := ecommercePlanningBatchable(input); ok {
		result, err = runEcommercePlanningBatches(ctx, input, draft)
	} else {
		result, err = runTextTask(ctx, input)
	}
	if err == nil {
		err = s.validateEcommerceWorkbenchResult(userID, input, result)
	}
	if err == nil && promptTemplateOperation != "" {
		err = validatePromptTemplateResult(promptTemplateOperation, result)
	}
	return result, err
}

func resolveProviderPriceTierModel(config providerConfig, channelModel *model.ChannelModel, modelKey, requestedModel string) (string, error) {
	providerModelKey := strings.TrimPrefix(strings.TrimSpace(config.ProviderModelKey), "models/")
	if config.PriceTierID != "" {
		for _, tier := range channelModel.PriceTiers {
			if tier.ID == config.PriceTierID && tier.Enabled && tier.PriceConfigured {
				return firstNonEmpty(providerModelKey, tier.ProviderModelKey), nil
			}
		}
		return "", errors.New("当前模型规格价格档已更新，请重新创建任务")
	}
	if modelKey != "" && requestedModel != "" && modelKey != requestedModel {
		return "", errors.New("系统渠道模型标识不一致")
	}
	return providerModelKey, nil
}
