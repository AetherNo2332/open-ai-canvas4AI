package architecture

import (
	"testing"
)

// TestModelGormWriteDetectionCoversAliasesAndChains is the negative fixture:
// persistence code placed back into internal/model must be reported with the
// exact file and line, including an aliased gorm import and chained builders.
func TestModelGormWriteDetectionCoversAliasesAndChains(t *testing.T) {
	body := `package model

import (
	g "gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Counter struct {
	ID    int   ` + "`gorm:\"primaryKey\"`" + `
	Value int64 ` + "`gorm:\"not null\"`" + `
}

func Append(tx *g.DB, value int64) error {
	counter := Counter{ID: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
		return err
	}
	if err := tx.Model(&Counter{}).Where("id = ?", 1).UpdateColumn("value", g.Expr("value + 1")).Error; err != nil {
		return err
	}
	return tx.Create(&Counter{ID: int(value)}).Error
}
`
	path := "internal/model/appender.go"
	root := testBackend(t, map[string]string{path: body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		path + ":15:model_gorm_write": true,
		path + ":18:model_gorm_write": true,
		path + ":21:model_gorm_write": true,
	}
	got := map[string]bool{}
	for _, finding := range findings {
		if finding.Rule == "model_gorm_write" {
			got[finding.ID] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("model gorm write findings = %v, want %v (all findings: %+v)", got, want, findings)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("missing model gorm write finding %s in %v", id, got)
		}
	}
}

func TestModelPackageLevelGormHandleIsDetected(t *testing.T) {
	path := "internal/model/store.go"
	body := `package model

import "gorm.io/gorm"

var db *gorm.DB

func Save(value *Counter) error {
	return db.Save(value)
}
`
	root := testBackend(t, map[string]string{path: body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != path+":8:model_gorm_write" {
		t.Fatalf("package-level gorm handle write not reported at its line: %+v", findings)
	}
}

func TestModelLocalGormHandleFromMultiReturnIsDetected(t *testing.T) {
	path := "internal/model/store.go"
	body := `package model

import "gorm.io/gorm"

func Save() error {
	db, err := gorm.Open(nil)
	if err != nil {
		return err
	}
	return db.Create(&struct{}{}).Error
}
`
	root := testBackend(t, map[string]string{path: body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != path+":10:model_gorm_write" {
		t.Fatalf("local gorm handle from a multi-return call not reported: %+v", findings)
	}
}

// TestPureModelDefinitionsAndTagsAreAllowed is the positive fixture: structs,
// gorm tags, a gorm field type and a same-named non-gorm Create method stay
// valid, proving the rule resolves receivers instead of matching method names.
func TestPureModelDefinitionsAndTagsAreAllowed(t *testing.T) {
	body := `package model

import "gorm.io/gorm"

type AgentEventCounter struct {
	ID        int   ` + "`gorm:\"primaryKey;autoIncrement:false\"`" + `
	Value     int64 ` + "`gorm:\"not null\"`" + `
	DeletedAt gorm.DeletedAt ` + "`gorm:\"index\"`" + `
}

func (AgentEventCounter) TableName() string { return "agent_event_counters" }

type Builder struct{}

func (Builder) Create(*AgentEventCounter) error { return nil }

func Touch(b Builder, counter *AgentEventCounter) error { return b.Create(counter) }
`
	root := testBackend(t, map[string]string{"internal/model/counter.go": body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("plain model definitions must pass: %+v", findings)
	}
}

func TestModelRawSQLIsRejected(t *testing.T) {
	path := "internal/model/store.go"
	body := `package model

import "gorm.io/gorm"

func Purge(db *gorm.DB) error {
	return db.Raw("DELETE FROM tasks").Scan(&struct{}{}).Error
}
`
	root := testBackend(t, map[string]string{path: body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != path+":6:model_gorm_write" {
		t.Fatalf("raw SQL escape hatch not reported: %+v", findings)
	}
}

func TestGormWritesOutsideModelRemainAllowed(t *testing.T) {
	body := `package repository

import "gorm.io/gorm"

func Save(db *gorm.DB, value any) error { return db.Save(value) }
`
	root := testBackend(t, map[string]string{"internal/repository/store.go": body})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("repository persistence must remain allowed: %+v", findings)
	}
}

func TestModelGormWriteFindingsCannotBeBaselined(t *testing.T) {
	path := "internal/model/store.go"
	root := testBackend(t, map[string]string{path: `package model

import "gorm.io/gorm"

func Save(db *gorm.DB, value any) error { return db.Save(value) }
`})
	id := path + ":5:model_gorm_write"
	findings, err := Check(root, Baseline{Version: 1, Allowances: []Allowance{{ID: id, Max: 1, Reason: "legacy model write"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != id || findings[0].Rule != "model_gorm_write" {
		t.Fatalf("model gorm write allowance must not hide the finding: %+v", findings)
	}
}
