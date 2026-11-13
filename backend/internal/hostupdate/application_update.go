package hostupdate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func (m *Manager) runUpdate(fromVersion, targetVersion string) {
	nextCompose, err := m.prepareTargetCompose(targetVersion)
	if err != nil {
		m.failWithoutRollback(PhaseFailed, err)
		return
	}
	defer os.Remove(nextCompose)
	if err := m.preflight(nextCompose, targetVersion); err != nil {
		m.failWithoutRollback(PhaseFailed, err)
		return
	}
	updaterBinary, err := m.prepareUpdaterBinary(targetVersion)
	if err != nil {
		m.failWithoutRollback(PhaseFailed, err)
		return
	}
	if updaterBinary != "" {
		defer os.Remove(updaterBinary)
	}

	m.setPhase(PhasePulling, "拉取目标版本镜像")
	targetImages, err := m.pullApplicationImages(nextCompose, targetVersion)
	if err != nil {
		m.failWithoutRollback(PhaseFailed, err)
		return
	}

	m.setPhase(PhaseDraining, "停止 Web、Agent 与 Backend，等待后台任务优雅退出")
	if err := m.stopApplicationServices(fromVersion, m.config.StepTimeout); err != nil {
		m.failBeforeMigration(err, fromVersion)
		return
	}

	// 最终备份必须包含旧服务退出前的全部写入；镜像下载不占用停服窗口。
	m.setPhase(PhaseBackingUp, "创建停服后的 PostgreSQL 与数据目录 ZIP 备份")
	backup, err := m.createBackup(fromVersion)
	if err != nil {
		m.failBeforeMigration(err, fromVersion)
		return
	}
	if err := replaceFile(m.composePath(), m.previousComposePath(), 0o600); err != nil {
		m.failBeforeMigration(fmt.Errorf("保存旧 Compose 配置：%w", err), fromVersion)
		return
	}
	if err := replaceFile(m.envPath(), m.previousEnvPath(), 0o600); err != nil {
		m.failBeforeMigration(fmt.Errorf("保存旧部署环境：%w", err), fromVersion)
		return
	}
	m.mu.Lock()
	m.state.LastBackup = &backup
	m.state.RollbackVersion = fromVersion
	_ = m.saveStateLocked()
	m.mu.Unlock()

	m.setPhase(PhaseMigrating, "执行目标版本数据库迁移")
	if err := m.composeWithImages(nextCompose, targetVersion, targetImages, m.config.StepTimeout, nil, "run", "--rm", "migrate"); err != nil {
		m.failWithRollback(err, fromVersion, backup)
		return
	}

	m.setPhase(PhaseSwitching, "切换 Compose 配置与镜像版本")
	if err := setDeploymentImages(m.envPath(), targetVersion, targetImages); err != nil {
		m.failWithRollback(err, fromVersion, backup)
		return
	}
	if err := replaceFile(nextCompose, m.composePath(), 0o600); err != nil {
		_ = setEnvValue(m.envPath(), "CANVAS_IMAGE_TAG", strings.TrimPrefix(fromVersion, "v"))
		m.failWithRollback(err, fromVersion, backup)
		return
	}
	if err := m.startBusiness(m.composePath(), targetVersion, targetImages); err != nil {
		m.failWithRollback(err, fromVersion, backup)
		return
	}

	m.setPhase(PhaseVerifying, "验证健康状态、运行版本和稳定窗口")
	if err := m.verifyHealthy(targetVersion); err != nil {
		m.failWithRollback(err, fromVersion, backup)
		return
	}
	if updaterBinary != "" {
		if err := replaceFile(updaterBinary, m.config.BinaryPath, 0o755); err != nil {
			m.mu.Lock()
			now := time.Now().UTC()
			m.state.Operation.Phase = PhaseManualIntervention
			m.state.Operation.FinishedAt = &now
			m.state.Operation.Error = "应用已更新，但 Host Updater 自更新失败：" + safeOperationError(err)
			m.appendLogLocked(PhaseManualIntervention, "应用版本已切换，更新器二进制需要人工更新")
			_ = m.saveStateLocked()
			m.mu.Unlock()
			return
		}
	}
	m.completeApplicationUpdate(targetVersion, updaterBinary)
}

func (m *Manager) completeApplicationUpdate(targetVersion, updaterBinary string) {
	docsErr := m.syncApplicationDocs()
	m.mu.Lock()
	now := time.Now().UTC()
	m.state.Operation.Phase = PhaseSucceeded
	m.state.Operation.FinishedAt = &now
	m.state.Operation.Error = ""
	m.appendLogLocked(PhaseSucceeded, fmt.Sprintf("已成功更新到 %s", targetVersion))
	if docsErr != nil {
		m.state.Operation.Phase = PhaseManualIntervention
		m.state.Operation.Error = "业务版本已切换，文档接入需处理：" + docsErr.Error()
		m.appendLogLocked(PhaseManualIntervention, m.state.Operation.Error)
	}
	if updaterBinary != "" {
		m.appendLogLocked(PhaseSucceeded, "Host Updater 二进制已同步到目标 Release")
	}
	_ = m.saveStateLocked()
	m.mu.Unlock()
	if updaterBinary != "" && m.config.ServiceName != "" {
		go m.restartSelf()
	}
}

func (m *Manager) pullApplicationImages(nextCompose, targetVersion string) (deploymentImages, error) {
	targetImages := immutableImageRefs(m.config.Repository, targetVersion)
	docsImage, err := m.applicationDocs(nextCompose, targetVersion)
	if err != nil {
		return deploymentImages{}, err
	}
	targetImages.docs = docsImage
	if docsImage != "" {
		ctx, cancel := context.WithTimeout(context.Background(), m.config.StepTimeout)
		_, err = m.docsDocker(ctx, "pull", docsImage)
		if err == nil {
			_, err = m.inspectDocs(ctx, docsImage)
		}
		cancel()
		if err != nil {
			return deploymentImages{}, err
		}
	}
	if err := m.composeWithImages(nextCompose, targetVersion, targetImages, m.config.StepTimeout, nil, "pull", "backend", "web", "agent"); err != nil {
		return deploymentImages{}, err
	}
	targetImages, err = m.verifyImages(targetVersion)
	if err != nil {
		return deploymentImages{}, err
	}
	targetImages.docs = docsImage

	return targetImages, nil
}
