package main

import (
	"encoding/json"
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/model"
	"os"
	"time"
)

func main() {
	if os.Getenv("CREW_ACCEPTANCE_LOCAL") != "1" {
		panic("local guard required")
	}
	db, err := gorm.Open(sqlite.Open("file:/data/open_ai_canvas.db?mode=ro"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	must(err)
	runID := os.Getenv("CREW_RUN_ID")
	if runID == "" {
		panic("run ID required")
	}
	var members []model.AgentCrewMemberRun
	must(db.Select("id", "role", "agent_run_id", "status").Where("crew_run_id = ?", runID).Find(&members).Error)
	seen := map[string]bool{}
	rows := []map[string]any{}
	type bounds struct {
		First time.Time
		Last  time.Time
	}
	windows := []bounds{}
	for _, member := range members {
		var execution model.CloudAgentExecution
		must(db.Select("id", "user_id", "conversation_id", "engine", "status").Where("id = ?", member.AgentRunID).First(&execution).Error)
		var session model.CloudAgentPiSession
		must(db.Select("id", "revision", "conversation_id").Where("user_id = ? AND conversation_id = ?", execution.UserID, execution.ConversationID).First(&session).Error)
		var entries []model.CloudAgentPiEntry
		must(db.Select("created_at").Where("session_id = ? AND run_id = ?", session.ID, execution.ID).Order("sequence").Find(&entries).Error)
		if seen[session.ID] || execution.Engine != "pi" || len(entries) == 0 {
			panic("independent live Pi session check failed")
		}
		seen[session.ID] = true
		rows = append(rows, map[string]any{"memberRunId": member.ID, "agentRunId": execution.ID, "role": member.Role, "status": execution.Status, "conversationId": execution.ConversationID, "piSessionId": session.ID, "revision": session.Revision, "entryCount": len(entries)})
		if member.Role == model.CrewMemberRoleMember {
			var messages []model.AgentCrewMessage
			must(db.Select("kind", "created_at").Where("crew_run_id = ? AND member_run_id = ?", runID, member.ID).Order("created_at").Find(&messages).Error)
			var window bounds
			for _, message := range messages {
				if message.Kind == "task" {
					window.First = message.CreatedAt
				}
				if message.Kind == "result" {
					window.Last = message.CreatedAt
				}
			}
			if window.First.IsZero() || window.Last.IsZero() {
				panic("structured task/result timing absent")
			}
			windows = append(windows, window)
		}
	}
	if len(members) != 3 || len(windows) != 2 {
		panic("three-member fixture expected")
	}
	overlap := windows[0].First.Before(windows[1].Last) && windows[1].First.Before(windows[0].Last)
	if !overlap {
		panic("member task execution windows do not overlap")
	}
	result := map[string]any{"runId": runID, "independentPiSessions": true, "parallelTaskWindows": overlap, "members": rows}
	body, err := json.MarshalIndent(result, "", "  ")
	must(err)
	fmt.Println(string(body))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
