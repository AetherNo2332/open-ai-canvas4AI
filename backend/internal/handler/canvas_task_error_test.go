package handler

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
	"yingce/backend/internal/app"
	"yingce/backend/internal/kernel"
)

func TestCanvasAdmissionHTTPKeepsSafeErrorOnly(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	for _, status := range []int{400, 403, 429} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		err := kernel.NewAppError(status, "所选模型已停用")
		err.Cause = kernel.NewAppError(500, "PRIVATE-FIXTURE-SECRET")
		canvasCommandError(c, err)
		if w.Code != status || !strings.Contains(w.Body.String(), "所选模型已停用") || strings.Contains(w.Body.String(), "PRIVATE-FIXTURE-SECRET") {
			t.Fatalf("unsafe admission projection: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	canvasCommandError(c, app.NewModelError(app.ErrCodeInvalidModelSelection, "所选模型已停用"))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_model_selection") {
		t.Fatal("model admission reason lost", w.Code, w.Body.String())
	}
}
