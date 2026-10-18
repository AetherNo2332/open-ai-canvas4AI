package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

// Admission and transactional writeback both recheck the marker: undo may
// restore a legacy document after a generation was prepared for approval.
func TestCloudAgentMediaLegacyCanvasRequiresMigration(t *testing.T) {
	for _, stage := range []string{"prepare", "writeback"} {
		t.Run(stage, func(t *testing.T) {
			s, db, args := agentMediaFixtureWithReferenceVersion(t, 0)
			before := agentCanvasPayload(t, s, "agent-canvas")
			policy, err := s.RuntimePolicy()
			if err != nil {
				t.Fatal(err)
			}
			if stage == "prepare" {
				run, state := agentMediaPiRun(t, s, db, args, "auto")
				_, _, err = s.prepareCloudAgentMedia(run, &state, agentMediaCall(args))
			} else {
				args.DraftRunID = "prepared-before-undo"
				err = createCloudAgentMediaNode(s.repo, "user", "agent-canvas", &cloudAgentMediaPlan{Args: args}, nil, policy)
			}
			agentAssertMigrationRequired(t, err)
			if got := agentCanvasPayload(t, s, "agent-canvas"); got != before {
				t.Fatal("rejected generation changed the legacy document")
			}
			var count int64
			if err = db.Model(&model.Task{}).Where("type IN ?", []string{"canvas_image", "canvas_video", "canvas_audio"}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("rejected generation created %d media tasks", count)
			}
		})
	}
}
