package main

import (
	"errors"
	"reflect"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func migrateIDSequences() tableMigration {
	return tableMigration{name: "id_sequences", run: func(source, target *gorm.DB, copyRows bool) (int, error) {
		if copyRows {
			return migrateSeededTable[model.IDSequence]("id_sequences").run(source, target, true)
		}
		return verifyIDSequences(source, target)
	}}
}

func verifyIDSequences(source, target *gorm.DB) (int, error) {
	var original, actual []model.IDSequence
	if err := source.Order("name").Find(&original).Error; err != nil {
		return 0, err
	}
	if err := target.Order("name").Find(&actual).Error; err != nil {
		return 0, err
	}
	if len(original) != len(actual) {
		return 0, errors.New("源数据与目标序列行数不一致")
	}
	for i, expected := range original {
		floor, known, err := database.PrefixedIDSequenceFloor(source, expected.Name)
		if err != nil {
			return 0, err
		}
		if known && floor > expected.Value {
			// Reconcile changes only a lagging value and its update timestamp.
			// It may not advance beyond the exact source business-ID floor.
			expected.Value = floor
			expected.UpdatedAt = actual[i].UpdatedAt
		}
		if !equivalent(reflect.ValueOf(expected), reflect.ValueOf(actual[i])) {
			return 0, errors.New("源数据与目标序列核对不一致")
		}
	}
	return len(original), nil
}
