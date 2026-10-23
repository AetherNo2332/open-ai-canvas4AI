package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"infinite-canvas/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

func TestCanvasCommandRevisionCASConflictHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	if !canvasCommandError(ctx, repository.ErrCanvasRevisionConflict) || recorder.Code != 409 {
		t.Fatalf("CAS response %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || payload.Data.Reason != "revision_conflict" {
		t.Fatalf("CAS reason: %s %v", recorder.Body.String(), err)
	}
}
