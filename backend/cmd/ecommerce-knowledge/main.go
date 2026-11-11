// Administrative maintenance only. This executable never starts generation workers.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"time"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func main() {
	sync := flag.Bool("sync", false, "sync the pinned, license-checked source allowlist")
	bootstrap := flag.Bool("bootstrap-builtin", false, "insert or upgrade shipped KB01-KB10 entries; local edits and review status are preserved")
	bundled := flag.Bool("bootstrap-bundled", false, "initialize the offline knowledge snapshot without overwriting existing records")
	adminID := flag.String("admin", "", "existing administrator ID, required for writes")
	flag.Parse()
	if os.Getenv("DATABASE_URL") == "" && os.Getenv("CANVAS_BACKEND_DATA_DIR") == "" {
		log.Fatal("explicit database location required")
	}
	driver := os.Getenv("CANVAS_DATABASE_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	dir := os.Getenv("CANVAS_BACKEND_DATA_DIR")
	db, err := database.Open(database.Config{Driver: driver, DSN: os.Getenv("DATABASE_URL"), DataDir: dir})
	if err != nil {
		log.Fatal(err)
	}
	if err := database.RequireSchemaVersion(db); err != nil {
		log.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()
	repo := repository.New(db)
	svc := service.New(repo, dir)
	defer svc.Close()
	var actor *model.User
	if *bootstrap || *bundled || *sync {
		actor, err = knowledgeWriteActor(repo, svc, *adminID)
		if err != nil {
			log.Fatal(err)
		}
	}
	if *bundled {
		if err := bootstrapBundledKnowledge(svc); err != nil {
			log.Fatal(err)
		}
	}
	if *bootstrap {
		if err := bootstrapKnowledge(svc); err != nil {
			log.Fatal(err)
		}
	}
	if *sync {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		result, err := svc.SyncEcommerceKnowledge(ctx, actor)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			log.Fatal(err)
		}
	}
	stats, err := svc.EcommerceKnowledgeStats()
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(stats); err != nil {
		log.Fatal(err)
	}
}

func knowledgeWriteActor(repo *repository.Repository, svc *service.Service, id string) (*model.User, error) {
	actor, err := repo.User(id)
	if err != nil || svc.RequireAdmin(actor) != nil {
		return nil, errors.New("an existing administrator is required for knowledge writes")
	}
	return actor, nil
}

func bootstrapKnowledge(svc *service.Service) error {
	report, err := svc.EnsureBuiltinEcommerceKnowledge()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"bootstrap": report}); err != nil {
		return err
	}
	industry, err := svc.EnsureIndustryTaxonomyKnowledge()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"industryTaxonomy": industry}); err != nil {
		return err
	}
	return nil
}

func bootstrapBundledKnowledge(svc *service.Service) error {
	report, err := svc.EnsureBundledEcommerceKnowledge()
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"bundle": report}); err != nil {
		return err
	}
	return nil
}
