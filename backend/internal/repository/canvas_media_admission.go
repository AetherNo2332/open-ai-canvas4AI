package repository

import "yingce/backend/internal/model"

func (r *Repository) CanvasMediaTaskMaterializationPending(owner, task string) (bool, error) {
	if !r.db.Migrator().HasTable(&model.CanvasTaskBinding{}) {
		return false, nil
	}
	binding, err := r.CanvasTaskBinding(task)
	if r.IsMissing(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if binding.OwnerID != owner {
		return false, nil
	}
	job, err := r.CanvasMaterialization(task)
	if r.IsMissing(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return job.State != "done" || job.Outcome == "media_save_pending", nil
}
