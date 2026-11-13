package hostupdate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (m *Manager) CheckDocs(ctx context.Context) (DocsStatus, error) {
	m.mu.Lock()
	if m.state.Operation.Phase.Active() || m.docsBusyLocked() {
		status := cloneDocsStatus(m.docs)
		m.mu.Unlock()
		return status, errors.New("已有更新操作正在进行")
	}
	m.docs.Operation = DocsOperation{ID: randomID(), Phase: PhaseChecking}
	if err := m.saveDocsLocked(); err != nil {
		m.mu.Unlock()
		return DocsStatus{}, err
	}
	m.mu.Unlock()
	candidate, current, err := m.discoverDocs(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.docs.Operation.Phase = PhaseFailed
		m.docs.Operation.Error = err.Error()
	} else {
		m.docs.Candidate = candidate
		m.docs.Current = current
		m.docs.Operation.Phase = PhaseReady
	}
	if saveErr := m.saveDocsLocked(); saveErr != nil {
		return cloneDocsStatus(m.docs), saveErr
	}
	return cloneDocsStatus(m.docs), err
}

func (m *Manager) discoverDocs(ctx context.Context) (*DocsBuild, *DocsBuild, error) {
	ctx, cancel := context.WithTimeout(ctx, m.config.StepTimeout)
	defer cancel()
	exists, contractErr := docsContract(m.composePath())
	if contractErr != nil {
		return nil, nil, contractErr
	}
	if !exists {
		return nil, nil, errors.New("当前编排没有受管 docs 服务")
	}
	values, err := readEnvFile(m.envPath())
	if err != nil {
		return nil, nil, err
	}
	current, err := m.inspectDocs(ctx, values["CANVAS_DOCS_IMAGE"])
	if err != nil {
		return nil, nil, err
	}
	ref := m.docsRepository() + ":docs-main"
	if _, err = m.docsDocker(ctx, "pull", ref); err != nil {
		return nil, nil, err
	}
	data, err := m.docsDocker(ctx, "image", "inspect", ref, "--format", "{{json .RepoDigests}}")
	if err != nil {
		return nil, nil, err
	}
	var digests []string
	if err = json.Unmarshal(data, &digests); err != nil {
		return nil, nil, err
	}
	for _, digest := range digests {
		if m.validDocsDigest(digest) != nil {
			continue
		}
		candidate, err := m.inspectDocs(ctx, digest)
		if err != nil {
			return nil, nil, err
		}
		return &candidate, &current, nil
	}
	return nil, nil, errors.New("文档通道没有有效仓库摘要")
}

func (m *Manager) StartDocsUpdate(request DocsUpdateRequest) (DocsStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startDocsLocked(request, false)
}
func (m *Manager) StartDocsRollback(request DocsRollbackRequest) (DocsStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(request.Reason) == "" || m.docs.Previous == nil {
		return cloneDocsStatus(m.docs), errors.New("缺少回退原因或上一成功文档版本")
	}
	return m.startDocsLocked(DocsUpdateRequest{TargetDigest: m.docs.Previous.Digest, ExpectedCurrentDigest: request.ExpectedCurrentDigest}, true)
}

func (m *Manager) startDocsLocked(request DocsUpdateRequest, rollback bool) (DocsStatus, error) {
	if m.state.Operation.Phase.Active() || m.docsBusyLocked() {
		return cloneDocsStatus(m.docs), errors.New("已有更新操作正在进行或待人工处理")
	}
	if err := m.validDocsDigest(request.TargetDigest); err != nil {
		return cloneDocsStatus(m.docs), err
	}
	exists, contractErr := docsContract(m.composePath())
	if contractErr != nil {
		return cloneDocsStatus(m.docs), contractErr
	}
	if !exists {
		return cloneDocsStatus(m.docs), errors.New("当前编排没有受管 docs 服务")
	}
	values, err := readEnvFile(m.envPath())
	if err != nil {
		return cloneDocsStatus(m.docs), err
	}
	current := values["CANVAS_DOCS_IMAGE"]
	if m.validDocsDigest(current) != nil {
		return cloneDocsStatus(m.docs), errors.New("当前部署没有受管 docs 服务")
	}
	if current != request.ExpectedCurrentDigest {
		return cloneDocsStatus(m.docs), errors.New("当前文档版本已变化，请重新检查")
	}
	if current == request.TargetDigest {
		return cloneDocsStatus(m.docs), errors.New("目标已是当前文档")
	}
	source := m.docs.Current
	if source == nil || source.Digest != current {
		source = &DocsBuild{Digest: current}
	}
	m.docs.Supported = true
	m.docs.Operation = DocsOperation{ID: randomID(), Phase: PhasePulling, Source: source, Target: &DocsBuild{Digest: request.TargetDigest}}
	if rollback {
		m.docs.Operation.Phase = PhaseRollingBack
	}
	if err = m.saveDocsLocked(); err != nil {
		return cloneDocsStatus(m.docs), err
	}
	go m.runDocsUpdate()
	return cloneDocsStatus(m.docs), nil
}

func (m *Manager) runDocsUpdate() {
	m.mu.Lock()
	op := m.docs.Operation
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), m.config.StepTimeout)
	defer cancel()
	_, err := m.docsDocker(ctx, "pull", op.Target.Digest)
	var target, source DocsBuild
	if err == nil {
		target, err = m.inspectDocs(ctx, op.Target.Digest)
	}
	if err == nil {
		source, err = m.inspectDocs(ctx, op.Source.Digest)
	}
	if err != nil {
		m.finishDocs(PhaseFailed, err)
		return
	}
	m.mu.Lock()
	m.docs.Operation.Source = &source
	m.docs.Operation.Target = &target
	// Persist the recovery intent before changing the env or container.
	m.docs.Operation.Switched = true
	m.docs.Operation.Phase = PhaseSwitching
	err = m.saveDocsLocked()
	m.mu.Unlock()
	if err != nil {
		m.finishDocs(PhaseManualIntervention, err)
		return
	}
	if err = setEnvValue(m.envPath(), "CANVAS_DOCS_IMAGE", target.Digest); err == nil {
		err = m.recreateDocs()
	}
	if err == nil {
		err = m.verifyDocs(target)
	}
	if err != nil {
		m.restoreDocs(err)
		return
	}
	m.mu.Lock()
	m.docs.Previous = &source
	m.docs.Current = &target
	m.docs.Operation.Phase = PhaseSucceeded
	err = m.saveDocsLocked()
	if err != nil {
		m.docs.Operation.Phase = PhaseManualIntervention
		m.docs.Operation.Error = "文档已切换，但持久化状态失败"
	}
	m.mu.Unlock()
}

func (m *Manager) finishDocs(phase Phase, cause error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs.Operation.Phase = phase
	if cause != nil {
		m.docs.Operation.Error = cause.Error()
	}
	if err := m.saveDocsLocked(); err != nil {
		m.docs.Operation.Phase = PhaseManualIntervention
		m.docs.Operation.Error = "文档状态写入失败，需要检查实际容器"
	}
}

func (m *Manager) restoreDocs(cause error) {
	m.mu.Lock()
	op := m.docs.Operation
	m.mu.Unlock()
	if op.Source == nil {
		m.finishDocs(PhaseManualIntervention, errors.New("缺少文档恢复源"))
		return
	}
	err := setEnvValue(m.envPath(), "CANVAS_DOCS_IMAGE", op.Source.Digest)
	if err == nil {
		err = m.recreateDocs()
	}
	if err == nil {
		err = m.verifyDocs(*op.Source)
	}
	if err != nil {
		m.finishDocs(PhaseManualIntervention, errors.New("文档恢复失败，需要检查实际容器"))
		return
	}
	m.mu.Lock()
	m.docs.Current = op.Source
	m.mu.Unlock()
	m.finishDocs(PhaseRolledBack, cause)
}

func (m *Manager) recoverDocs() {
	m.mu.Lock()
	op := m.docs.Operation
	m.mu.Unlock()
	if op.Switched {
		m.restoreDocs(errors.New("更新器重启后恢复上一成功文档"))
		return
	}
	m.finishDocs(PhaseFailed, errors.New("更新器在文档切换前退出，当前文档未修改"))
}
