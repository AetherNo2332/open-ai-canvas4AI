package app

import (
	"context"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/skills"
)

// piSnapshotToolSpecs lists the frozen catalog tools with whether the current
// permission and advertised loadout still allow each one.
func piSnapshotToolSpecs(state *cloudAgentRuntime) []PiAgentToolSpec {
	tools := make([]PiAgentToolSpec, 0, len(state.Canonical.Tools))
	for _, item := range state.Canonical.Tools {
		function, _ := item["function"].(map[string]any)
		name := stringField(function, "name")
		parameters, _ := function["parameters"].(map[string]any)
		if name == "" || parameters == nil {
			continue
		}
		nativeRead := state.SkillRuntimeMode == cloudAgentSkillRuntimeNative && name == "read"
		allowed := nativeRead || (cloudAgentToolAllowed(state.Request, name) && cloudAgentToolAdvertisedForRun(state, name))
		tools = append(tools, PiAgentToolSpec{
			Name: name, Description: stringField(function, "description"), Parameters: parameters,
			Category: cloudAgentToolCategory(name), Allowed: allowed,
		})
	}
	return tools
}

func piSnapshotPendingCompaction(state *cloudAgentRuntime) *PiPendingContextCompaction {
	compaction := state.ContextCompaction
	if compaction == nil || compaction.PiOperationID == "" {
		return nil
	}
	return &PiPendingContextCompaction{
		OperationID: compaction.PiOperationID, SessionRevision: compaction.PiSessionRevision,
		ActiveLeafID: compaction.PiSourceLeafID, Reason: compaction.PiReason,
		WillRetry: compaction.PiWillRetry, TokensBefore: compaction.PiTokensBefore,
	}
}

// piSnapshotNativeSkills loads the frozen native Skill snapshots. A permanently
// unavailable snapshot fails and cleans up the run before reporting the error.
func (s *Service) piSnapshotNativeSkills(run *model.CloudAgentExecution, state *cloudAgentRuntime, mode string) ([]PiSkillSnapshot, error) {
	if mode != cloudAgentSkillRuntimeNative {
		return nil, nil
	}
	nativeSkills, err := s.piNativeSkillSnapshots(run.UserID, state.Skills)
	if err == nil {
		return nativeSkills, nil
	}
	if !skills.IsPermanentFrozenSkillError(err) {
		return nil, err
	}
	message := "本轮固定的技能快照已不可用，请重新选择技能后发起新一轮"
	if failErr := s.failCloudAgent(run, state, message); failErr != nil {
		return nil, failErr
	}
	terminal, readErr := s.repo.CloudAgent(run.UserID, run.ID)
	if readErr != nil {
		return nil, readErr
	}
	if cleanupErr := s.finishCloudAgentCleanup(context.Background(), terminal); cleanupErr != nil {
		return nil, cleanupErr
	}
	return nil, kernel.BadAuthRequest(message)
}
