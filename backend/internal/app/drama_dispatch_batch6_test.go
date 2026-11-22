package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestDramaSegmentPromptBudgetFailureKeepsProgressOpen(t *testing.T) {
	svc, fake := newDramaTestService(t)
	video := dramaVideoFixture()
	video.References.PromptMaxChars = 250
	setDramaVideoCapability(t, svc, fake, video)
	project := createTestDramaWithMedia(t, svc, DramaReviewAuto, DramaMediaManual).Project.ID

	runDramaTicks(t, svc, project, 20)

	if got := fake.byKind["segment_prompt"]; got != 0 {
		t.Fatalf("budget failure must not submit prompt writers: %d", got)
	}
	summary, err := svc.DramaSummary("usr-drama", project)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Production.Status == model.DramaStatusCompleted {
		t.Fatal("a failed prompt must not mark the production completed")
	}
	found := false
	for _, episode := range summary.Episodes {
		segments, err := svc.repo.DramaSegments(episode.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, segment := range segments {
			if segment.PromptStatus == model.DramaMediaFailed && strings.Contains(segment.PromptError, "放不下本段正文") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the segment must keep the prompt budget failure visible")
	}
}
