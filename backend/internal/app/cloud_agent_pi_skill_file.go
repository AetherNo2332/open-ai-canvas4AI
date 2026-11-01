package app

import (
	"fmt"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type PiSkillFileRequest struct {
	NativeName string
	Path       string
	Offset     int
	Limit      int
}

func (s *Service) PiSkillFile(userID, runID, owner string, request PiSkillFileRequest) (*PiSkillReadPage, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, err
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, err
	}
	if state.SkillRuntimeMode != cloudAgentSkillRuntimeNative {
		return nil, kernel.Forbidden("该运行使用旧版 Skill 工具")
	}
	path, err := normalizeNativeSkillReadPath(request.Path)
	if err != nil {
		return nil, err
	}
	if request.Offset < 0 || request.Limit < 0 || request.Limit > piNativeSkillReadMaxRunes {
		return nil, BadAuthRequest("Skill read range is invalid")
	}
	skill, listed, err := piSkillFileTarget(&state, request.NativeName, path)
	if err != nil {
		return nil, err
	}
	if err := s.piChargeSkillReadBudget(userID, runID, run, &state, skill, path, request); err != nil {
		return nil, err
	}
	file, err := s.cloudAgentFrozenSkillFile(userID, *skill, path)
	if err != nil {
		return nil, err
	}
	if file.Binary {
		return nil, BadAuthRequest("Only text Skill files can be read")
	}
	if file.File.SHA256 != listed.SHA256 || file.File.Size != listed.Size {
		return nil, kernel.Forbidden("Skill file differs from the frozen snapshot")
	}
	return piSkillFilePage(&state, skill, path, file, request)
}

// piSkillFileTarget finds the enabled Skill and the listed text file the read
// targets in the run's frozen snapshot.
func piSkillFileTarget(state *cloudAgentRuntime, nativeName, path string) (*cloudAgentSkill, *PiSkillFile, error) {
	var skill *cloudAgentSkill
	for index := range state.Skills {
		candidate := &state.Skills[index]
		if candidate.NativeName == nativeName {
			skill = candidate
			break
		}
	}
	if skill == nil {
		return nil, nil, kernel.Forbidden("Skill is not enabled for this run")
	}
	if path != cloudAgentSkillEntryPath && !isNativeSkillTextPath(path) {
		return nil, nil, BadAuthRequest("Only text Skill files can be read")
	}
	var listed *PiSkillFile
	for index := range skill.NativeFiles {
		if skill.NativeFiles[index].Path == path && skill.NativeFiles[index].Text {
			listed = &skill.NativeFiles[index]
			break
		}
	}
	if listed == nil {
		return nil, nil, kernel.Forbidden("Skill file is not listed for this run")
	}
	return skill, listed, nil
}

// piChargeSkillReadBudget counts a new read range against the run's read
// budget once per step; exhausting the budget fails the run.
func (s *Service) piChargeSkillReadBudget(userID, runID string, run *model.CloudAgentExecution, state *cloudAgentRuntime, skill *cloudAgentSkill, path string, request PiSkillFileRequest) error {
	limits, err := s.cloudAgentStepLimits()
	if err != nil {
		return err
	}
	state.CallLimits = limits.CallLimits
	budgetKey := fmt.Sprintf("%s:%s:%s:%d:%d", state.LastStepTaskID, skill.NativeName, path, request.Offset, request.Limit)
	if state.NativeReadBudgetKeys[budgetKey] {
		return nil
	}
	if state.ReadToolCalls >= state.callLimits().ReadCallsPerRun {
		budgetErr := &cloudAgentReadLoopError{ToolName: "read", Count: state.ReadToolCalls + 1, Budget: true}
		if err := s.failCloudAgent(run, state, budgetErr.Error()); err != nil {
			return err
		}
		return kernel.Forbidden(budgetErr.Error())
	}
	state.ReadToolCalls++
	if state.NativeReadBudgetKeys == nil {
		state.NativeReadBudgetKeys = map[string]bool{}
	}
	state.NativeReadBudgetKeys[budgetKey] = true
	if err := s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	}); err != nil {
		return err
	}
	return nil
}

// piSkillFilePage pages the frozen file content; the entry file is normalized
// first and reports the entry digest instead of the raw file hash.
func piSkillFilePage(state *cloudAgentRuntime, skill *cloudAgentSkill, path string, file *SkillPackageFileContent, request PiSkillFileRequest) (*PiSkillReadPage, error) {
	content := file.Content
	var err error
	if path == cloudAgentSkillEntryPath {
		entrySkill := *skill
		entrySkill.Instruction = content
		content, err = normalizePiSkillEntry(entrySkill)
		if err != nil {
			return nil, err
		}
	}
	page, err := nativeSkillReadPage(content, request.Offset, request.Limit)
	if err != nil {
		return nil, err
	}
	page.Path = path
	page.cloudAgentReadBudget = state.readBudget()
	page.NativeName, page.SkillID, page.VersionID, page.ContentHash = skill.NativeName, skill.ID, firstNonEmpty(skill.VersionID, skill.Version), skill.Hash
	page.IsEntry = path == cloudAgentSkillEntryPath
	if page.IsEntry {
		page.SHA256, _, err = nativeSkillEntryDigest(cloudAgentSkill{ID: skill.ID, Name: skill.Name, NativeName: skill.NativeName, Description: skill.Description, Instruction: file.Content})
		if err != nil {
			return nil, err
		}
	} else {
		page.SHA256 = file.File.SHA256
	}
	return &page, nil
}

func (s *Service) piNativeSkillSnapshots(userID string, skills []cloudAgentSkill) ([]PiSkillSnapshot, error) {
	snapshots, err := piSkillSnapshots(skills)
	if err != nil {
		return nil, BadAuthRequest("Invalid frozen Skill metadata")
	}
	for index, skill := range skills {
		entry, err := s.cloudAgentFrozenSkillFile(userID, skill, cloudAgentSkillEntryPath)
		if err != nil {
			return nil, err
		}
		if entry.Binary {
			return nil, BadAuthRequest("Skill entry file is not text")
		}
		snapshot := &snapshots[index]
		skill.Instruction = entry.Content
		snapshot.EntryContent, err = normalizePiSkillEntry(skill)
		if err != nil {
			return nil, BadAuthRequest("Invalid frozen Skill entry")
		}
		for index := range snapshot.Files {
			if snapshot.Files[index].Path != cloudAgentSkillEntryPath {
				continue
			}
			snapshot.Files[index].SHA256, snapshot.Files[index].Size, err = nativeSkillEntryDigest(skill)
			if err != nil {
				return nil, err
			}
		}
	}
	return snapshots, nil
}
