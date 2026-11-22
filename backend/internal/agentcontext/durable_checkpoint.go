package agentcontext

import (
	"errors"
	"fmt"
)

// JournalEvent 是持久化事件窗口与执行行对账所需的最小身份投影。
// RowSequence 来自身份记录的行序号，Sequence 来自事件正文。
type JournalEvent struct {
	RowSequence int
	Sequence    int
	RunID       string
	EventID     string
}

// ValidateMessageCount 校验已载入 transcript 是否覆盖执行行声明的水位。
func ValidateMessageCount(rows, count int) error {
	if rows != count {
		return fmt.Errorf("agent execution transcript is incomplete: rows=%d count=%d", rows, count)
	}
	return nil
}

// ValidateJournalEvent 校验单条持久化事件的 row/body 序号、run 与 event ID 是否自洽。
func ValidateJournalEvent(runID string, event JournalEvent) error {
	if !validJournalIdentity(runID, event.RowSequence, event) {
		return errors.New("agent event identity is invalid")
	}
	return nil
}

// ValidateJournalWindow 校验已载入事件窗口的身份、尾序号与水位。
// 返回值是窗口起始水位，供下一次追加事件继续使用。
func ValidateJournalWindow(runID string, eventCount int, events []JournalEvent) (int, error) {
	for _, event := range events {
		if err := ValidateJournalEvent(runID, event); err != nil {
			return 0, err
		}
	}
	return JournalWatermark(eventCount, events)
}

// JournalWatermark 校验窗口尾序号与声明水位，并返回窗口起始水位。
func JournalWatermark(eventCount int, events []JournalEvent) (int, error) {
	// 窗口为空但水位非零 = 有行没读到，不能把“一行都没有”当成空日志。
	if len(events) == 0 && eventCount != 0 {
		return 0, errors.New("agent execution journal is incomplete")
	}
	if len(events) > 0 && events[len(events)-1].Sequence != eventCount {
		return 0, fmt.Errorf("agent execution journal is incomplete: lastSeq=%d count=%d", events[len(events)-1].Sequence, eventCount)
	}
	base := eventCount - len(events)
	if base < 0 {
		return 0, fmt.Errorf("agent execution journal watermark is invalid: count=%d rows=%d", eventCount, len(events))
	}
	return base, nil
}

// ValidateJournalSequence 校验待保存窗口从既有水位开始连续编号，且身份自洽。
func ValidateJournalSequence(runID string, eventSeqBase int, events []JournalEvent) error {
	for index, event := range events {
		sequence := eventSeqBase + index + 1
		if event.Sequence != sequence || event.RunID != runID || event.EventID != fmt.Sprintf("%s:%d", runID, sequence) {
			return errors.New("agent event sequence or identity is invalid")
		}
	}
	return nil
}

func validJournalIdentity(runID string, sequence int, event JournalEvent) bool {
	return event.Sequence == event.RowSequence &&
		event.RunID == runID &&
		event.EventID == fmt.Sprintf("%s:%d", runID, sequence)
}
