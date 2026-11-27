package repository

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// W02：项目内整数资产身份必须带作品身份。两个项目可以各有资产 1，
// 任何跨项目读写都必须落空，建档失败必须整笔回滚。

func newShortplayTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:shortplay-identity-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.Project{},
		&model.Shortplay{},
		&model.ShortplayEpisode{},
		&model.ShortplayAsset{},
		&model.ShortplayAssetImage{},
		&model.ShortplayAssetAudio{},
		&model.ShortplayJob{},
	); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func seedShortplay(t *testing.T, repo *Repository, userID, projectID, shortplayID, name string, assets []model.ShortplayAsset) {
	t.Helper()
	now := time.Now()
	project := &model.Project{ID: projectID, UserID: userID, Name: name, Type: "shortplay", AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
	row := &model.Shortplay{ID: shortplayID, ProjectID: projectID, UserID: userID, SettingsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	episode := model.ShortplayEpisode{ID: shortplayID + "-ep1", ShortplayID: shortplayID, Number: 1, Title: "第一集", CanvasID: "canvas-" + shortplayID, CreatedAt: now, UpdatedAt: now}
	if err := repo.CreateShortplayProject(project, row, []model.ShortplayEpisode{episode}); err != nil {
		t.Fatalf("seed shortplay %s: %v", shortplayID, err)
	}
	for index := range assets {
		assets[index].ShortplayID = shortplayID
		if err := repo.CreateShortplayAsset(&assets[index]); err != nil {
			t.Fatalf("seed asset %d of %s: %v", assets[index].ID, shortplayID, err)
		}
	}
}

func TestShortplayAssetIdentityIsProjectScoped(t *testing.T) {
	repo, _ := newShortplayTestRepository(t)
	seedShortplay(t, repo, "user-1", "project-a", "sp-a", "雨夜回眸", []model.ShortplayAsset{{ID: 1, Kind: "character", Name: "林小满"}})
	seedShortplay(t, repo, "user-1", "project-b", "sp-b", "天台之后", []model.ShortplayAsset{{ID: 1, Kind: "character", Name: "顾言"}})

	assetA, err := repo.ShortplayAssetByID("sp-a", 1)
	if err != nil {
		t.Fatalf("read sp-a asset 1: %v", err)
	}
	if assetA.Name != "林小满" {
		t.Fatalf("sp-a asset 1 name = %q, want 林小满", assetA.Name)
	}
	assetB, err := repo.ShortplayAssetByID("sp-b", 1)
	if err != nil {
		t.Fatalf("read sp-b asset 1: %v", err)
	}
	if assetB.Name != "顾言" {
		t.Fatalf("sp-b asset 1 name = %q, want 顾言", assetB.Name)
	}

	if _, err := repo.ShortplayAssetByID("sp-a", 2); !errors.Is(err, ErrShortplayAssetNotFound) {
		t.Fatalf("cross-project read err = %v, want ErrShortplayAssetNotFound", err)
	}

	nextA, err := repo.NextShortplayAssetID("sp-a")
	if err != nil {
		t.Fatal(err)
	}
	if nextA != 2 {
		t.Fatalf("sp-a next asset id = %d, want 2", nextA)
	}
}

func TestShortplayAssetWriteRequiresMatchingProject(t *testing.T) {
	repo, db := newShortplayTestRepository(t)
	seedShortplay(t, repo, "user-1", "project-a", "sp-a", "雨夜回眸", []model.ShortplayAsset{{ID: 1, Kind: "character", Name: "林小满"}})
	seedShortplay(t, repo, "user-1", "project-b", "sp-b", "天台之后", []model.ShortplayAsset{
		{ID: 1, Kind: "character", Name: "顾言"},
		{ID: 2, Kind: "character", Name: "顾言-旧"},
	})

	// 用 sp-a 的作品身份改「只存在于 sp-b 的 id 2」：复合主键不匹配，必须 0 行受影响。
	row := model.ShortplayAsset{ShortplayID: "sp-a", ID: 2, Kind: "character", Name: "被覆盖"}
	applied, err := repo.UpdateShortplayAsset(&row, 0)
	if err != nil {
		t.Fatalf("cross-project write err = %v", err)
	}
	if applied {
		t.Fatal("cross-project write applied, want false")
	}
	var assetB model.ShortplayAsset
	if err := db.First(&assetB, "shortplay_id = ? AND id = ?", "sp-b", 2).Error; err != nil {
		t.Fatal(err)
	}
	if assetB.Name != "顾言-旧" {
		t.Fatalf("sp-b asset 2 was overwritten: name = %q", assetB.Name)
	}

	// 同项目内 revision CAS 生效：期望版本 0 命中，写入后版本为 1。
	own := model.ShortplayAsset{ShortplayID: "sp-a", ID: 1, Kind: "character", Name: "林小满-改"}
	applied, err = repo.UpdateShortplayAsset(&own, 0)
	if err != nil || !applied {
		t.Fatalf("own write applied=%v err=%v, want true, nil", applied, err)
	}
	stale, err := repo.UpdateShortplayAsset(&own, 0)
	if err != nil || stale {
		t.Fatalf("stale write applied=%v err=%v, want false, nil", stale, err)
	}
}

func TestCreateShortplayProjectRollsBackWholeTransaction(t *testing.T) {
	repo, db := newShortplayTestRepository(t)
	now := time.Now()
	project := &model.Project{ID: "project-1", UserID: "user-1", Name: "雨夜回眸", Type: "shortplay", AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
	row := &model.Shortplay{ID: "sp-1", ProjectID: "project-1", UserID: "user-1", SettingsJSON: "{}", CreatedAt: now, UpdatedAt: now}

	// 两个集行共用同一个画布地址：唯一约束在写集行时失败，项目与域配置必须一起回滚。
	episodes := []model.ShortplayEpisode{
		{ID: "ep-1", ShortplayID: "sp-1", Number: 1, Title: "第一集", CanvasID: "canvas-1", CreatedAt: now, UpdatedAt: now},
		{ID: "ep-2", ShortplayID: "sp-1", Number: 2, Title: "第二集", CanvasID: "canvas-1", CreatedAt: now, UpdatedAt: now},
	}
	if err := repo.CreateShortplayProject(project, row, episodes); err == nil {
		t.Fatal("expected duplicate canvas id to fail the transaction")
	}
	for _, countRow := range []struct {
		table string
		want  int64
	}{{"projects", 0}, {"shortplays", 0}, {"shortplay_episodes", 0}} {
		var count int64
		if err := db.Table(countRow.table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != countRow.want {
			t.Fatalf("%s rows after rollback = %d, want %d", countRow.table, count, countRow.want)
		}
	}

	// 同名重复建档：唯一约束转成业务错误，不留下第二份域配置。
	okProject := &model.Project{ID: "project-2", UserID: "user-1", Name: "雨夜回眸", Type: "shortplay", AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
	okShortplay := &model.Shortplay{ID: "sp-2", ProjectID: "project-2", UserID: "user-1", SettingsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := repo.CreateShortplayProject(okProject, okShortplay, nil); err != nil {
		t.Fatalf("first create: %v", err)
	}
	dupProject := &model.Project{ID: "project-3", UserID: "user-1", Name: "雨夜回眸", Type: "shortplay", AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
	dupShortplay := &model.Shortplay{ID: "sp-3", ProjectID: "project-3", UserID: "user-1", SettingsJSON: "{}", CreatedAt: now, UpdatedAt: now}
	err := repo.CreateShortplayProject(dupProject, dupShortplay, nil)
	if !errors.Is(err, ErrShortplayNameTaken) {
		t.Fatalf("duplicate name err = %v, want ErrShortplayNameTaken", err)
	}
	var shortplayCount int64
	if err := db.Model(&model.Shortplay{}).Count(&shortplayCount).Error; err != nil {
		t.Fatal(err)
	}
	if shortplayCount != 1 {
		t.Fatalf("shortplays after duplicate = %d, want 1", shortplayCount)
	}
}

func TestCreateShortplayProjectConcurrentNameCollisionHasSingleWinner(t *testing.T) {
	repo, db := openShortplayPostgresTestRepository(t)
	now := time.Now()
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			project := &model.Project{ID: fmt.Sprintf("concurrent-project-%d", index), UserID: "user-1", Name: "并发同名", Type: "shortplay", AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
			row := &model.Shortplay{ID: fmt.Sprintf("concurrent-sp-%d", index), ProjectID: project.ID, UserID: "user-1", SettingsJSON: "{}", CreatedAt: now, UpdatedAt: now}
			episode := model.ShortplayEpisode{ID: fmt.Sprintf("concurrent-ep-%d", index), ShortplayID: row.ID, Number: 1, Title: "第一集", CanvasID: fmt.Sprintf("concurrent-canvas-%d", index), CreatedAt: now, UpdatedAt: now}
			results <- repo.CreateShortplayProject(project, row, []model.ShortplayEpisode{episode})
		}()
	}
	succeeded, nameTaken := 0, 0
	for index := 0; index < 2; index++ {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrShortplayNameTaken):
			nameTaken++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if succeeded != 1 || nameTaken != 1 {
		t.Fatalf("succeeded=%d nameTaken=%d, want 1/1", succeeded, nameTaken)
	}
	for _, countRow := range []struct {
		table string
		want  int64
	}{{"projects", 1}, {"shortplays", 1}, {"shortplay_episodes", 1}} {
		var count int64
		if err := db.Table(countRow.table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != countRow.want {
			t.Fatalf("%s rows after concurrent create = %d, want %d", countRow.table, count, countRow.want)
		}
	}
}

// openShortplayPostgresTestRepository 在真实 PostgreSQL 的隔离 schema 里建短剧表。
// 没有 DSN 时跳过：SQLite 单写者会以 table is locked 拒绝并发写，不能用来证明并发建档的赢家唯一。
func openShortplayPostgresTestRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("CANVAS_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	adminSQL, _ := admin.DB()
	t.Cleanup(func() { _ = adminSQL.Close() })
	schema := fmt.Sprintf("shortplay_repo_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error; err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	testDSN, err := shortplayPostgresDSNWithSchema(dsn, schema)
	if err != nil {
		t.Fatalf("test dsn: %v", err)
	}
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test schema: %v", err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Project{}, &model.Shortplay{}, &model.ShortplayEpisode{}, &model.ShortplayAsset{}, &model.ShortplayAssetImage{}, &model.ShortplayAssetAudio{}, &model.ShortplayJob{}); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func shortplayPostgresDSNWithSchema(dsn, schema string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
func TestListShortplayItemsCountsEpisodes(t *testing.T) {
	repo, _ := newShortplayTestRepository(t)
	seedShortplay(t, repo, "user-1", "project-a", "sp-a", "雨夜回眸", nil)
	now := time.Now()
	extra := model.ShortplayEpisode{ID: "sp-a-ep2", ShortplayID: "sp-a", Number: 2, Title: "第二集", CanvasID: "canvas-sp-a-2", CreatedAt: now, UpdatedAt: now}
	if err := repo.db.Create(&extra).Error; err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListShortplayItems("user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].EpisodeCount != 2 || items[0].Name != "雨夜回眸" {
		t.Fatalf("items = %+v, want one item with 2 episodes", items)
	}
	other, err := repo.ListShortplayItems("user-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other user sees %d shortplays, want 0", len(other))
	}
}
