package desktop

import (
	"crypto/subtle"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/identity"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const UpstreamBaseline = "f1d8ee00e1bb3c7a22c1d8ce374275372eb337b3"

type RouterConfig struct {
	Tasks          *tasks.Service
	Commands       *commands.Service
	Owner          identity.Principal
	ProfileID      string
	WebDir         string
	InstanceID     string
	Origin         string
	BootstrapToken string
}

func respond(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": data, "msg": "ok"})
}

func deny(c *gin.Context, status int) {
	c.AbortWithStatusJSON(status, gin.H{"code": status, "data": nil, "msg": http.StatusText(status)})
}

func staticFile(webDir, relative string) (string, bool) {
	root, err := filepath.EvalSymlinks(webDir)
	if err != nil {
		return "", false
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", false
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	info, err := os.Stat(target)
	return target, err == nil && info.Mode().IsRegular()
}

func NewRouter(svc *app.Service, cfg RouterConfig) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), handler.RequestCorrelationMiddleware())
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		if cfg.Origin != "" && ("http://"+c.Request.Host != cfg.Origin || (c.GetHeader("Origin") != "" && c.GetHeader("Origin") != cfg.Origin)) {
			deny(c, 403)
			return
		}
		c.Next()
	})
	api := r.Group("/api")
	api.GET("/health", func(c *gin.Context) {
		respond(c, gin.H{"ready": true, "mode": "desktop", "baseline": UpstreamBaseline, "desktopSchema": database.DesktopSchemaVersion, "instanceId": cfg.InstanceID})
	})
	var bootstrapMu sync.Mutex
	bootstrapToken := cfg.BootstrapToken
	expires := time.Now().Add(time.Minute)
	api.POST("/desktop/session", func(c *gin.Context) {
		bootstrapMu.Lock()
		defer bootstrapMu.Unlock()
		candidate := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if bootstrapToken == "" || time.Now().After(expires) || subtle.ConstantTimeCompare([]byte(candidate), []byte(bootstrapToken)) != 1 {
			deny(c, 401)
			return
		}
		session, err := svc.IssueLocalSession(cfg.Owner.ID)
		if err != nil {
			deny(c, 500)
			return
		}
		bootstrapToken = ""
		http.SetCookie(c.Writer, &http.Cookie{Name: auth.SessionCookieName, Value: session.Session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: session.MaxAgeSecs})
		respond(c, gin.H{"user": session.User})
	})
	protected := api.Group("")
	protected.Use(func(c *gin.Context) {
		cookie, _ := c.Cookie(auth.SessionCookieName)
		user, err := svc.CurrentUser(cookie)
		if err != nil || user.ID != cfg.Owner.ID {
			deny(c, 401)
			return
		}
		c.Next()
	})
	protected.GET("/auth/session", func(c *gin.Context) {
		cookie, _ := c.Cookie(auth.SessionCookieName)
		user, err := svc.CurrentUser(cookie)
		if err != nil {
			deny(c, 401)
			return
		}
		respond(c, gin.H{"user": user})
	})
	protected.GET("/desktop/bootstrap", func(c *gin.Context) {
		features, err := svc.FeatureAvailability()
		if err != nil {
			deny(c, 500)
			return
		}
		drawing, err := svc.DrawingEngineSetting()
		if err != nil {
			deny(c, 500)
			return
		}
		limits, err := svc.PublicRuntimeLimits()
		if err != nil {
			deny(c, 500)
			return
		}
		respond(c, gin.H{"instanceId": cfg.InstanceID, "profileId": cfg.ProfileID, "principal": cfg.Owner, "features": features, "drawingEngine": drawing, "runtimeLimits": limits, "capabilities": gin.H{"protocol": 1, "saveMode": "legacy-single-writer", "mcp": false, "collaboration": false}})
	})
	// Deliberate domain registration; no website auth, payment or Agent router.
	handler.ConfigureRuntime(svc)
	if cfg.Commands != nil {
		handler.RegisterCanvasCommandRoutes(protected, cfg.Commands, func(*gin.Context) (identity.Principal, error) { return cfg.Owner, nil })
	}
	if cfg.Tasks != nil {
		handler.RegisterCanvasTaskRoutes(protected, cfg.Tasks, func(*gin.Context) (identity.Principal, error) { return cfg.Owner, nil })
	}
	RegisterSettingsRoutes(protected, svc)
	handler.RegisterSystemProxyRoutes(protected, svc)
	handler.RegisterAppearanceRoutes(protected, svc)
	handler.RegisterFeatureAvailabilityRoutes(protected, svc)
	handler.RegisterChannelModelRoutes(protected, svc)
	handler.RegisterLogicalModelRoutes(protected, svc)
	handler.RegisterModelCatalogRoutes(protected, svc)
	handler.RegisterTaskRoutes(protected, svc)
	handler.RegisterRunningHubRoutes(protected, svc)
	handler.RegisterSkillRoutes(protected, svc)
	handler.RegisterUserDataRoutes(protected, svc)
	handler.RegisterChunkedUploadRoutes(protected, svc)
	handler.RegisterPluginRoutes(protected, svc)
	handler.RegisterLibraryRoutes(protected, svc)
	handler.RegisterProjectRoutes(protected, svc)
	handler.RegisterLibTVRoutes(protected, svc)
	handler.RegisterTapNowRoutes(protected, svc)
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if c.Request.Method != http.MethodGet || path == "/api" || strings.HasPrefix(path, "/api/") || strings.ContainsAny(path, "\\:\x00") {
			deny(c, 404)
			return
		}
		for _, segment := range strings.Split(path, "/") {
			if segment == ".." || segment == "." {
				deny(c, 404)
				return
			}
		}
		if target, ok := staticFile(cfg.WebDir, strings.TrimPrefix(path, "/")); ok {
			c.File(target)
			return
		}
		if path == "/" || path == "/canvas" || strings.HasPrefix(path, "/canvas/") || path == "/assets" || path == "/tasks" || path == "/skills" || path == "/plugins" || path == "/projects" || strings.HasPrefix(path, "/projects/") || path == "/settings" || strings.HasPrefix(path, "/settings/") {
			c.Header("Cache-Control", "no-store")
			if target, ok := staticFile(cfg.WebDir, "index.html"); ok {
				c.File(target)
				return
			}
		}
		deny(c, 404)
	})
	return r
}
