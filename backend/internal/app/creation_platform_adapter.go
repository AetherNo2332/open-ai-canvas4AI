package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// adapterSubmission 是业务适配器（工作台、短剧、画布）经平台提交一条已解析任务的入参。
// 适配器在自己的解析器里完成业务校验（工具、模板、剧情段），平台负责幂等、准入、冻结、事件与快照。
type adapterSubmission struct {
	UserID   string
	Business cp.BusinessRef
	// Key 为空表示不做幂等（每次新建任务），用于保留“再生成一次”的旧语义。
	Key      string
	TaskReq  CreateTaskRequest
	DramaJob *model.DramaJob
}

// submitResolved 走与 Submit 相同的准备、提交与回放流程；返回落库后的任务行。
func (p *creationPlatform) submitResolved(in adapterSubmission) (*model.Task, bool, error) {
	application, err := platformApplication(in.Business.Application)
	if err != nil {
		return nil, false, err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = "auto:" + newID()
	}
	fingerprint, err := resolvedTaskFingerprint(in.TaskReq)
	if err != nil {
		return nil, false, err
	}
	lookup := func() (*cp.SubmitReceipt, error) {
		return p.existingPlatformSubmission(in.UserID, application, key, fingerprint)
	}
	if replay, err := lookup(); replay != nil || err != nil {
		return p.replayedTask(in.UserID, replay, err)
	}
	task, order, err := p.prepareResolvedTask(in.UserID, in.TaskReq)
	if err == nil {
		err = p.commitPlatformTask(task, order, adapterSubmissionRecord(in, application, key, fingerprint), in.DramaJob)
	}
	if err != nil {
		replay, lookupErr := replayConcurrentWinner(lookup, err)
		if replay == nil && lookupErr != nil {
			return nil, false, lookupErr
		}
		return p.replayedTask(in.UserID, replay, lookupErr)
	}
	return taskForOutput(*task), false, nil
}

func (p *creationPlatform) replayedTask(userID string, replay *cp.SubmitReceipt, err error) (*model.Task, bool, error) {
	if err != nil {
		return nil, false, err
	}
	task, err := p.s.repo.TaskForUser(userID, replay.Snapshot.TaskID)
	if err != nil {
		return nil, false, err
	}
	return taskForOutput(*task), true, nil
}

// resolvedTaskFingerprint 摘要已解析请求的生成内容；追踪 ID 不参与，map 键序列化有序。
func resolvedTaskFingerprint(req CreateTaskRequest) (string, error) {
	encoded, err := json.Marshal([]any{req.Type, req.Operation, strings.TrimSpace(req.Prompt), req.Model, req.LogicalModelID, req.Input})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func adapterSubmissionRecord(in adapterSubmission, application, key, fingerprint string) *model.PlatformSubmission {
	business, _ := json.Marshal(in.Business)
	return &model.PlatformSubmission{
		UserID: in.UserID, Application: application, IdempotencyKey: key, Fingerprint: fingerprint,
		Capability: capabilityFromTaskType(in.TaskReq.Type), BusinessJSON: string(business),
		ProjectRef: in.Business.ProjectID, ObjectType: in.Business.ObjectType, ObjectID: in.Business.ObjectID, OperationID: in.Business.OperationID,
	}
}
