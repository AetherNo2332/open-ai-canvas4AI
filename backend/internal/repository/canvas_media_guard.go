package repository

import (
	"errors"
	"yingce/backend/internal/model"
)

var ErrCanvasMediaVersionManaged = errors.New("媒体版本记录不可改写，请新增版本或切换版本")

// guardMediaVersionRecords 对所有画布写入生效：节点可以新增、删除、切换版本（上游浏览器的正常操作），
// 但已存在的版本记录不能换成另一份媒体。
func guardMediaVersionRecords(before, after map[string]any) error {
	if before != nil && model.CanvasMediaVersionRecordChanged(before, after) {
		return ErrCanvasMediaVersionManaged
	}
	return nil
}
