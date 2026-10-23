package repository

import (
	"context"
	"database/sql"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Keep live library filtering separate from raw reads used by restore,
// durable receipts and terminal-state observers. No epoch means legacy/live.
func (r *Repository) liveCanvasProjectsQuery() (*gorm.DB, error) {
	predicate, err := r.canvasLivePredicate("canvas_projects")
	if err != nil {
		return nil, err
	}
	return r.db.Model(&model.CanvasProject{}).Where(predicate), nil
}

func (r *Repository) canvasLivePredicate(canvasTable string) (clause.Expr, error) {
	present, err := r.hasCommandSchema()
	if err != nil {
		return clause.Expr{}, err
	}
	if !present {
		return clause.Expr{SQL: "1=1"}, nil
	}
	deleted := r.db.Table(r.db.NamingStrategy.TableName("CanvasDocumentEpoch")+" AS live_epoch").Select("1").
		Where("live_epoch.owner_id = "+canvasTable+".user_id AND live_epoch.canvas_id = "+canvasTable+".id AND live_epoch.deleted = ?", true)
	return clause.Expr{SQL: "NOT EXISTS (?)", Vars: []any{deleted}}, nil
}

func (r *Repository) LiveCanvasProjectForUser(ownerID, canvasID string) (*model.CanvasProject, error) {
	query, err := r.liveCanvasProjectsQuery()
	if err != nil {
		return nil, err
	}
	var row model.CanvasProject
	if err = query.Where("user_id = ? AND id = ?", ownerID, canvasID).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) LiveCanvasProjectMetadataForUser(ownerID, canvasID string) (*model.CanvasProject, error) {
	query, err := r.liveCanvasProjectsQuery()
	if err != nil {
		return nil, err
	}
	var row model.CanvasProject
	if err = query.Omit("payload_json").Where("user_id = ? AND id = ?", ownerID, canvasID).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Call after establishing canvas ownership. CommandEpoch remains the raw
// terminal-state accessor; this projection permits legacy rows without epochs.
func (r *Repository) LiveCanvasEpoch(ownerID, canvasID string) (*model.CanvasDocumentEpoch, error) {
	epoch, err := r.CommandEpoch(ownerID, canvasID)
	if r.IsMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if epoch.Deleted {
		return nil, gorm.ErrRecordNotFound
	}
	return epoch, nil
}

// Status polling needs a coherent read snapshot, never a hot account UPDATE.
func (r *Repository) CanvasReadTransaction(ctx context.Context, fn func(*Repository) error) error {
	options := &sql.TxOptions{ReadOnly: true}
	if r.Dialect() == "postgres" {
		options.Isolation = sql.LevelRepeatableRead
	}
	return r.db.WithContext(ctx).Transaction(func(db *gorm.DB) error { return fn(r.transactionRepository(db)) }, options)
}
