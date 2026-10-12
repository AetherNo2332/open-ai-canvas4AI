package desktop

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/model"
	"io"
	"net/http"
)

func strictJSON(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		deny(c, 400)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		deny(c, 400)
		return false
	}
	return true
}
func settingsError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var typed *app.AppError
	if errors.As(err, &typed) && typed.Code >= 400 && typed.Code < 500 {
		c.AbortWithStatusJSON(typed.Code, gin.H{"code": typed.Code, "data": nil, "msg": typed.Message})
		return true
	}
	deny(c, 500)
	return true
}
func desktopActor(c *gin.Context, svc *app.Service) (*model.User, error) {
	cookie, _ := c.Cookie(auth.SessionCookieName)
	return svc.CurrentUser(cookie)
}
func RegisterSettingsRoutes(r *gin.RouterGroup, svc *app.Service) {
	r.GET("/desktop/settings/storage", func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		setting, err := svc.AdminOSSSetting(actor)
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"setting": setting})
	})
	r.PATCH("/desktop/settings/storage", func(c *gin.Context) {
		var req app.OSSSettingRequest
		if !strictJSON(c, &req) {
			return
		}
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		setting, err := svc.UpdateOSSSetting(actor, req)
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"setting": setting})
	})
	r.POST("/desktop/settings/storage/test", func(c *gin.Context) {
		var req app.OSSSettingRequest
		if !strictJSON(c, &req) {
			return
		}
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		result, err := svc.TestAdminOSSSetting(actor, req)
		if settingsError(c, err) {
			return
		}
		respond(c, result)
	})
	r.GET("/desktop/settings/appearance", func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		setting, err := svc.AdminAppearance(actor)
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"setting": setting})
	})
	r.PATCH("/desktop/settings/appearance", func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		current, err := svc.AdminAppearance(actor)
		if settingsError(c, err) {
			return
		}
		req := current.AppearanceSetting
		if !strictJSON(c, &req) {
			return
		}
		setting, err := svc.UpdateAppearance(actor, req)
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"setting": setting})
	})
	r.GET("/desktop/settings/protocols", func(c *gin.Context) {
		respond(c, gin.H{"protocols": svc.PluginProviderCatalog("admin.system-channel", "", false)})
	})
	r.GET("/desktop/settings/private-hosts", func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		hosts, err := svc.DesktopPrivateHosts(actor)
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"hosts": hosts})
	})
	r.PUT("/desktop/settings/private-hosts", func(c *gin.Context) {
		var req struct {
			Hosts []string `json:"hosts"`
		}
		if !strictJSON(c, &req) {
			return
		}
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) || settingsError(c, svc.SetDesktopPrivateHosts(actor, req.Hosts)) {
			return
		}
		respond(c, gin.H{"saved": true})
	})
	channels := func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		items, err := svc.DesktopChannels(actor)
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"channels": items})
	}
	r.GET("/desktop/settings/channels", channels)
	save := func(c *gin.Context) {
		var req app.ChannelRequest
		if !strictJSON(c, &req) {
			return
		}
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		if c.Param("channelId") == "" {
			_, err = svc.CreateSystemChannel(actor, req)
		} else {
			_, err = svc.UpdateSystemChannel(actor, c.Param("channelId"), req)
		}
		if settingsError(c, err) {
			return
		}
		channels(c)
	}
	r.POST("/desktop/settings/channels", save)
	r.PATCH("/desktop/settings/channels/:channelId", save)
	r.DELETE("/desktop/settings/channels/:channelId", func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) || settingsError(c, svc.DeleteSystemChannel(actor, c.Param("channelId"))) {
			return
		}
		channels(c)
	})
	saveModel := func(c *gin.Context) {
		var req app.ChannelModelRequest
		if !strictJSON(c, &req) {
			return
		}
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		item, err := svc.SaveDesktopChannelModel(actor, c.Param("channelId"), c.Param("modelId"), req)
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"model": item})
	}
	r.POST("/desktop/settings/channels/:channelId/models", saveModel)
	r.PATCH("/desktop/settings/channels/:channelId/models/:modelId", saveModel)
	r.DELETE("/desktop/settings/channels/:channelId/models/:modelId", func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) || settingsError(c, svc.DeleteAdminChannelModel(actor, c.Param("channelId"), c.Param("modelId"))) {
			return
		}
		channels(c)
	})
	r.GET("/desktop/settings/channels/:channelId/catalog", func(c *gin.Context) {
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		items, err := svc.PreviewAdminChannelModels(c.Request.Context(), actor, c.Param("channelId"))
		if settingsError(c, err) {
			return
		}
		respond(c, gin.H{"models": items})
	})
	r.POST("/desktop/settings/channels/:channelId/test", func(c *gin.Context) {
		var req app.ChannelModelRequest
		if !strictJSON(c, &req) {
			return
		}
		actor, err := desktopActor(c, svc)
		if settingsError(c, err) {
			return
		}
		result, err := svc.TestAdminChannelModel(c.Request.Context(), actor, c.Param("channelId"), req)
		if settingsError(c, err) {
			return
		}
		respond(c, result)
	})
	r.POST("/desktop/relay", func(c *gin.Context) { serveChannelRelay(c, svc) })
}
