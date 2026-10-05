package app

import (
	"fmt"
	"strings"
)

// 产出媒体文件的任务类型：这些任务成功后必须把结果回存为资源文件。
// 文本任务只增长任务历史数据，不占用账号文件容量。
func taskTypeProducesStoredFile(taskType string) bool {
	switch taskType {
	case "canvas_image", "canvas_video", "canvas_audio":
		return true
	}
	return strings.HasPrefix(taskType, "video_")
}

// requireStoredFileCapacityForTask 是生成任务 admission 阶段的账号文件容量校验。
//
// 账号文件容量此前只在产物回存时校验，上游调用已经发出并扣费，失败只能表现为
// 媒体无法入库。生成任务必须在扣费前确认账号还有可用容量，容量已满时直接拒绝。
func (s *Service) requireStoredFileCapacityForTask(userID string, taskType string, policy RuntimePolicySetting) error {
	if !taskTypeProducesStoredFile(taskType) {
		return nil
	}
	storedLimit := gigabytes(policy.Resource.StoredFileGB)
	if storedLimit <= 0 {
		return nil
	}
	storedBytes, err := s.repo.UserStoredFileBytes(userID)
	if err != nil {
		return err
	}
	// CreateTask 也会在已持有 storageMu 的 Agent 审批与入队事务内被调用
	// （DecideCloudAgentApproval / enqueueCloudAgentTask 持锁跨 CreateTask，而
	// sync.Mutex 不可重入）。TryLock 拿不到锁说明处于这类路径：该任务的容量已在
	// 未持锁的 dry admission 阶段校验过，此处退化为只按已存储字节复核，pending
	// 计数由持锁方在同一临界区内继续维护。
	if !s.storageMu.TryLock() {
		return validateStoredFileCapacity(storedBytes, 0, storedLimit)
	}
	defer s.storageMu.Unlock()
	return validateStoredFileCapacity(storedBytes, s.pendingStorage[userID], storedLimit)
}

func validateStoredFileCapacity(storedBytes int64, pendingBytes int64, storedLimit int64) error {
	if storedLimit > 0 && storedBytes+pendingBytes >= storedLimit {
		return QuotaExceeded(fmt.Sprintf("账号文件容量已达到 %s 上限，请先清理素材后再生成", formatStorageLimit(storedLimit)))
	}
	return nil
}
