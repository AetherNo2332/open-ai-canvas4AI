package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/repository"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	DataDir        string
	WebDir         string
	ListenAddr     string
	MasterKey      []byte
	Ready          io.Writer
	BootstrapToken string
}

func Run(ctx context.Context, cfg Config) error {
	if len(cfg.MasterKey) != 32 {
		return errors.New("desktop protected master key required")
	}
	if cfg.DataDir == "" {
		return errors.New("desktop data directory required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(cfg.WebDir, "index.html")); err != nil {
		return fmt.Errorf("desktop frontend unavailable: %w", err)
	}
	if len(cfg.BootstrapToken) < 40 {
		return errors.New("private bootstrap credential required")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("desktop listener must use a loopback IP")
	}
	dsn := filepath.Join(cfg.DataDir, "open_ai_canvas.db") + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=FULL"
	db, err := database.Open(database.Config{DataDir: cfg.DataDir, DSN: dsn})
	if err != nil {
		return err
	}
	sql, err := db.DB()
	if err != nil {
		return err
	}
	defer sql.Close()
	if err := database.ConfigurePool(db); err != nil {
		return err
	}
	if err := database.CheckDesktopStorage(db); err != nil {
		return err
	}
	// Website schema is temporarily retained for reused domains; B5 removes
	// dormant Agent models and replaces this with the desktop domain schema.
	if err := database.MigrateSchema(db); err != nil {
		return err
	}
	if err := database.MigrateDesktop(db); err != nil {
		return err
	}
	repo := repository.New(db)
	owner, err := BootstrapOwner(ctx, repo)
	if err != nil {
		return err
	}
	profile, err := repo.DesktopProfile()
	if err != nil {
		return err
	}
	svc, err := app.NewDesktop(repo, cfg.DataDir, StaticSecretProvider(cfg.MasterKey))
	if err != nil {
		return err
	}
	defer svc.Close()
	for _, initialize := range []func() error{svc.EnsureDefaultPromptTemplates, svc.EnsureBuiltinProjectWorkflowTemplate, svc.EnsureBuiltinSkills, svc.EnsureSkillPackages} {
		if err := initialize(); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	instance := kernel.NewID()
	commandService := commands.NewService(repo, commands.ServiceOptions{Validation: svc.DesktopCommandValidation})
	taskService := tasks.NewService(repo, commandService, svc.DesktopTaskSubmitter(), svc.DesktopCanvasTaskOptions())
	svc.AttachDesktopCanvasTasks(taskService)
	router := NewRouter(svc, RouterConfig{Commands: commandService, Tasks: taskService, Owner: owner, ProfileID: profile.ID, InstanceID: instance, Origin: origin, WebDir: cfg.WebDir, BootstrapToken: cfg.BootstrapToken})
	server := &http.Server{Handler: router, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	if err := svc.StartDesktopWorkers(ctx); err != nil {
		return err
	}
	defer func() {
		svc.BeginDrain()
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = svc.StopWorker(stop)
	}()
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	if cfg.Ready == nil {
		cfg.Ready = io.Discard
	}
	if err := json.NewEncoder(cfg.Ready).Encode(map[string]any{"type": "ready", "protocol": 1, "origin": origin, "instanceId": instance, "baseline": UpstreamBaseline}); err != nil {
		_ = server.Close()
		return err
	}
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-result:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return err
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}
