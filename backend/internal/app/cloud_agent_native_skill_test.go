package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func nativeSkillLeasedFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution, cloudAgentSkill) {
	t.Helper()
	s, db, run := piAgentTestLeasedFixture(t)
	skill, err := s.CreateSkill("user", SkillMutationRequest{SkillName: "Native", Description: "Use for scripts",
		Instruction: "# Before update\n", Tag: "others", IsPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := s.cloudAgentSkills("user", []string{skill.SkillID})
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.SkillRuntimeMode = cloudAgentSkillRuntimeNative
	state.Skills = snapshots
	state.Canonical.Tools = compileCloudAgentToolsForRuntime(state.Request, false, cloudAgentSkillRuntimeNative)
	if err := db.Model(run).Update("state_json", marshalNativeTestState(t, state)).Error; err != nil {
		t.Fatal(err)
	}
	return s, db, run, snapshots[0]
}

func marshalNativeTestState(t *testing.T, state cloudAgentRuntime) string {
	t.Helper()
	value, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func TestNativeSkillNameIsStableLegalAndCollisionResistant(t *testing.T) {
	first := nativeSkillName("技能/导演", "skill-1234567890abcdef")
	second := nativeSkillName("完全不同", "skill-1234567890abcdef")
	if first != second {
		t.Fatalf("native name changed for the same Skill ID: %q != %q", first, second)
	}
	if !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(first) {
		t.Fatalf("native name is not a Pi-safe slug: %q", first)
	}
	if len(first) > piNativeSkillNameMaxLength {
		t.Fatalf("native name length = %d, want <= %d", len(first), piNativeSkillNameMaxLength)
	}
	if first == nativeSkillName("另一个", "skill-1234567890abc0") {
		t.Fatal("different Skill IDs collided")
	}
}

func TestNativeSkillFrontmatterExactClosingDelimiter(t *testing.T) {
	for _, tc := range []struct{ name, input, body string }{
		{"eof", "---\nname: x\ndescription: y\n---", ""},
		{"crlf", "---\r\nname: x\r\ndescription: y\r\n---\r\n# Body\r\n", "# Body\r\n"},
		{"delimiter-prefix", "---\nname: x\n---suffix\nbody\n---\nkept", "kept"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() { if value := recover(); value != nil { t.Errorf("frontmatter panicked: %v", value) } }()
			entry, err := normalizePiSkillEntry(cloudAgentSkill{ID: "one", Name: "Native", Instruction: tc.input})
			if err != nil { t.Fatal(err) }
			body := strings.SplitN(entry, "\n---\n", 2)[1]
			if body != tc.body { t.Fatalf("body = %q, want %q", body, tc.body) }
		})
	}
}

func TestNativeMixedBatchAdmitsOnlyModelCanvasSubsetAndReplays(t *testing.T) {
	s, db, run, frozen := nativeSkillLeasedFixture(t)
	read := piAgentTestCall("read-1", "read", `{"path":"C:/old/skills/`+frozen.NativeName+`/SKILL.md"}`)
	canvas := piAgentTestCall("canvas-1", "canvas_get_state", `{}`)
	canvas2 := piAgentTestCall("canvas-2", "canvas_get_state", `{}`)
	output, _ := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{read, canvas, canvas2}})
	if err := db.Create(&model.Task{ID: "mixed-step", UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(output)}).Error; err != nil { t.Fatal(err) }
	state, _ := cloudAgentDecode(run)
	state.LastStepTaskID, state.ActiveTaskID = "mixed-step", "mixed-step"
	state.TaskIDs = append(state.TaskIDs, "mixed-step")
	if err := db.Model(run).Update("state_json", marshalNativeTestState(t, state)).Error; err != nil { t.Fatal(err) }
	assistant, _ := json.Marshal(map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "toolCall", "id": read.ID, "name": "read", "arguments": map[string]any{"path": "C:/old/skills/"+frozen.NativeName+"/SKILL.md"}},
		map[string]any{"type": "toolCall", "id": canvas.ID, "name": "canvas_get_state", "arguments": map[string]any{}},
		map[string]any{"type": "toolCall", "id": canvas2.ID, "name": "canvas_get_state", "arguments": map[string]any{}},
	}})
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 1, TaskID: "mixed-step", Message: assistant}); err != nil { t.Fatal(err) }
	for _, bad := range [][]cloudAgentCall{{canvas2, canvas}, {canvas}, {read, canvas, canvas2}} {
		if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: "mixed-step", Calls: bad}); err == nil { t.Fatal("tampered mixed batch admitted") }
	}
	batch := PiToolBatchRequest{TaskID: "mixed-step", Calls: []cloudAgentCall{canvas, canvas2}}
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil { t.Fatalf("mixed canvas subset: %v", err) }
	before, _ := reloadPiRun(t, s, run.ID)
	if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, batch); err != nil { t.Fatalf("recovery replay: %v", err) }
	after, _ := reloadPiRun(t, s, run.ID)
	if after.Revision != before.Revision { t.Fatal("replay mutated durable batch") }
	for _, call := range batch.Calls {
		receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, batch.TaskID, call.ID)
		if err != nil || receipt.Pending || receipt.IsError { t.Fatalf("canvas receipt = %+v, %v", receipt, err) }
	}
}

func TestNativePendingTaskReclaimUsesFencedViewAndPreservesOldWorkerCalls(t *testing.T) {
	s, db, run, frozen := nativeSkillLeasedFixture(t)
	_, state := reloadPiRun(t, s, run.ID)
	request, _ := piFirstStepRequest(state)
	request.Canonical.SystemPrompt += "\n<location>C:/temp/canvas-pi-run-old/cwd/skills/" + frozen.NativeName + "/SKILL.md</location>\n<cwd>\nC:/temp/canvas-pi-run-old/cwd\n</cwd>"
	step, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request)
	if err != nil { t.Fatal(err) }
	var before int64
	if err := db.Model(&model.Task{}).Count(&before).Error; err != nil { t.Fatal(err) }
	if err := db.Model(run).Update("lease_expires_at", nil).Error; err != nil { t.Fatal(err) }
	if err := db.Model(&model.CloudAgentPiSession{}).Where("active_run_id = ?", run.ID).Update("lease_expires_at", nil).Error; err != nil { t.Fatal(err) }
	snapshot, err := s.ClaimPiAgent("worker-b")
	if err != nil || snapshot == nil || snapshot.ActiveTask != step.TaskID { t.Fatalf("pending recovery: %+v %v", snapshot, err) }
	owner := fmt.Sprintf("worker-b@%d", snapshot.PiSessionLeaseEpoch)
	pending, err := s.PiModelStepView("user", run.ID, owner, step.TaskID)
	if err != nil || pending.Status != string(model.TaskStatusQueued) { t.Fatalf("pending view: %+v %v", pending, err) }
	for _, denied := range []string{"worker-a", fmt.Sprintf("worker-b@%d", snapshot.PiSessionLeaseEpoch-1)} {
		if _, err := s.PiModelStepView("user", run.ID, denied, step.TaskID); err == nil { t.Fatal("stale lease read task") }
	}
	if _, err := s.PiModelStepView("other", run.ID, owner, step.TaskID); err == nil { t.Fatal("cross-user task view") }
	call := piAgentTestCall("old-read", "read", `{"path":"C:/temp/canvas-pi-run-old/cwd/skills/`+frozen.NativeName+`/SKILL.md"}`)
	output, _ := json.Marshal(map[string]any{"toolCalls":[]cloudAgentCall{call}})
	if err := db.Model(&model.Task{}).Where("id = ?", step.TaskID).Updates(map[string]any{"status":model.TaskStatusSucceeded, "result_json":string(output)}).Error; err != nil { t.Fatal(err) }
	completed, err := s.PiModelStepView("user", run.ID, owner, step.TaskID)
	if err != nil || string(completed.Result) != string(output) { t.Fatalf("original model result changed: %+v %v", completed, err) }
	var after int64
	if err := db.Model(&model.Task{}).Count(&after).Error; err != nil { t.Fatal(err) }
	if after != before { t.Fatal("recovery created a second billed task") }
}

func TestNativeFailedReadCheckpointPairsRejectedPathsWithoutSuccessUsage(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false:"rejected-path", true:"allowed-path"}[valid], func(t *testing.T) {
			s, _, run, frozen := nativeSkillLeasedFixture(t)
			path := "C:/private/secret.txt"
			if valid { path = "C:/temp/skills/" + frozen.NativeName + "/SKILL.md" }
			call, _ := json.Marshal(map[string]any{"role":"assistant", "content": []any{map[string]any{"type":"toolCall", "id":"rejected-read", "name":"read", "arguments":map[string]any{"path":path}}}})
			result := json.RawMessage(`{"role":"toolResult","toolCallId":"rejected-read","toolName":"read","isError":true,"content":[{"type":"text","text":"Skill read rejected"}]}`)
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence:1, Message:call}); err != nil { t.Fatal(err) }
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence:2, Message:result}); err != nil { t.Fatalf("rejected read must be checkpointable: %v", err) }
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence:3, Message:result}); err == nil { t.Fatal("duplicate rejected result accepted") }
			_, state := reloadPiRun(t, s, run.ID)
			found := false
			for _, event := range state.Events {
				if event.Type == "native_skill_read" { t.Fatal("failed read emitted success") }
				if event.Type == "native_skill_read_failed" { found = true; encoded, _ := json.Marshal(event); if strings.Contains(string(encoded), "C:/") || strings.Contains(string(encoded), "secret.txt") { t.Fatalf("unsafe event: %s", encoded) } }
			}
			if !found { t.Fatal("missing distinct read failure event") }
			usage := cloudAgentSkillUsageFromEvents(state.Events)
			if valid && (len(usage.Skills) != 1 || usage.Skills[0].ReadCalls != 0 || usage.Skills[0].ReadFailures != 1) { t.Fatalf("failed usage: %+v", usage) }
		})
	}
}

func TestNormalizePiSkillEntryAddsSafeFrontmatterToLegacyInstruction(t *testing.T) {
	skill := cloudAgentSkill{
		ID:          "skill-1234567890abcdef",
		NativeName:  nativeSkillName("导演", "skill-1234567890abcdef"),
		Name:        "导演工作流",
		Description: "把想法拆成镜头",
		Instruction: "# 原始正文\n\n不能被修改。",
	}

	entry, err := normalizePiSkillEntry(skill)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(entry, "---\n") || !strings.Contains(entry, "name: \""+skill.NativeName+"\"\n") || !strings.Contains(entry, "description: \"把想法拆成镜头\"\n") {
		t.Fatalf("entry lacks expected frontmatter: %q", entry)
	}
	if !strings.HasSuffix(entry, skill.Instruction) {
		t.Fatalf("legacy instruction was not preserved: %q", entry)
	}
	if strings.Contains(entry, skill.ID) {
		t.Fatal("entry leaked the private Skill ID")
	}
}

func TestNativeSkillSnapshotCarriesFrozenVersionAndTextFileMetadata(t *testing.T) {
	snapshot, err := makePiSkillSnapshot(cloudAgentSkill{
		ID:          "skill-1234567890abcdef",
		NativeName:  "skill-1234567890abcdef",
		Name:        "导演工作流",
		Description: "拆镜头",
		VersionID:   "version-one",
		Version:     "1.0.0",
		Hash:        "hash-one",
		Instruction: "# Skill",
		Files: map[string]string{
			"SKILL.md":        "sha-entry",
			"references/a.md": "sha-reference",
			"scripts/run.js":  "sha-script",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.VersionID != "version-one" || snapshot.ContentHash != "hash-one" || snapshot.EntryPath != "SKILL.md" {
		t.Fatalf("frozen identity = %#v", snapshot)
	}
	if len(snapshot.Files) != 2 || snapshot.Files[0].Path != "SKILL.md" || snapshot.Files[1].Path != "references/a.md" {
		t.Fatalf("text allowlist = %#v", snapshot.Files)
	}
	if snapshot.Files[0].SHA256 != "sha-entry" || snapshot.Files[1].SHA256 != "sha-reference" {
		t.Fatalf("file hashes = %#v", snapshot.Files)
	}
}

func TestCloudAgentSkillsFreezeFileHashesWithoutBodies(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	skill, err := s.CreateSkill("user", SkillMutationRequest{SkillName: "Native", Description: "A test skill",
		Instruction: "# Stable body\n", Tag: "others", IsPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := s.cloudAgentSkills("user", []string{skill.SkillID})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].VersionID != skill.VersionID || snapshots[0].Hash != skill.ContentHash || snapshots[0].Instruction != "" {
		t.Fatalf("frozen Skill = %#v", snapshots)
	}
	if len(snapshots[0].NativeFiles) == 0 || snapshots[0].NativeFiles[0].SHA256 == "" || snapshots[0].NativeFiles[0].Size == 0 {
		t.Fatalf("missing immutable file metadata: %#v", snapshots[0].NativeFiles)
	}
}

func TestNativeRuntimeDoesNotExposeLegacySkillTools(t *testing.T) {
	req := CloudAgentRequest{SkillIDs: []string{"skill-1"}}
	tools := compileCloudAgentToolsForRuntime(req, true, cloudAgentSkillRuntimeNative)
	names := cloudAgentToolNames(tools)
	if slices.Contains(names, "skill_search") || slices.Contains(names, "skill_read_file") {
		t.Fatalf("legacy Skill tools exposed in native runtime: %v", names)
	}
	if !slices.Contains(names, "read") {
		t.Fatal("native read must be present in the Go model schema catalog")
	}

	legacy := compileCloudAgentToolsForRuntime(req, true, cloudAgentSkillRuntimeLegacy)
	legacyNames := cloudAgentToolNames(legacy)
	if !slices.Contains(legacyNames, "skill_search") || !slices.Contains(legacyNames, "skill_read_file") {
		t.Fatalf("legacy Skill tools missing from legacy runtime: %v", legacyNames)
	}
}

func TestNativeSkillReadPageRejectsTraversalAndBoundsOutput(t *testing.T) {
	if _, err := normalizeNativeSkillReadPath("../SKILL.md"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if _, err := normalizeNativeSkillReadPath("references/../../secret"); err == nil {
		t.Fatal("normalized traversal was accepted")
	}
	if _, err := nativeSkillReadPage("body", 0, piNativeSkillReadMaxRunes+1); err == nil {
		t.Fatal("oversized read was accepted")
	}
	page, err := nativeSkillReadPage(strings.Repeat("a", piNativeSkillReadMaxRunes+10), 0, piNativeSkillReadMaxRunes)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(page.Content)) != piNativeSkillReadMaxRunes || !page.HasMore {
		t.Fatalf("page bounds = %#v", page)
	}
}

func TestNativeSkillSnapshotIncludesDigestForMaterializedEntry(t *testing.T) {
	skill := cloudAgentSkill{ID: "skill-digest", Name: "Digest", Description: "Read me", Instruction: "# Body\n"}
	digest, size, err := nativeSkillEntryDigest(skill)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := normalizePiSkillEntry(skill)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(entry))
	if digest != hex.EncodeToString(want[:]) || size != int64(len(entry)) {
		t.Fatalf("materialized entry digest = %q size=%d", digest, size)
	}
}

func TestNativeSkillFileUsesFrozenVersionAndLease(t *testing.T) {
	s, _, run, frozen := nativeSkillLeasedFixture(t)
	request := PiSkillFileRequest{NativeName: frozen.NativeName, Path: "SKILL.md", Limit: 1000}
	page, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.Content, "# Before update") || page.VersionID != frozen.VersionID || !page.IsEntry || page.SHA256 == "" {
		t.Fatalf("frozen entry = %+v", page)
	}
	if _, err := s.PiSkillFile("other", run.ID, run.LeaseOwner, request); err == nil {
		t.Fatal("other user read Skill")
	}
	if _, err := s.PiSkillFile("user", run.ID, "other-worker", request); err == nil {
		t.Fatal("other worker read Skill")
	}
	for _, path := range []string{"../SKILL.md", "references/../../SKILL.md", "C:/secret", "scripts/run.js", "references/unlisted.md"} {
		request.Path = path
		if _, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request); err == nil {
			t.Fatalf("read accepted %q", path)
		}
	}
	request.Path = "SKILL.md"
	request.Limit = piNativeSkillReadMaxRunes + 1
	if _, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request); err == nil {
		t.Fatal("oversized read accepted")
	}
}

func TestNativeSkillSnapshotRejectsMissingFrozenPackage(t *testing.T) {
	s, db, run, _ := nativeSkillLeasedFixture(t)
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Skills[0].Hash = "wrong-hash"
	if err := db.Model(run).Update("state_json", marshalNativeTestState(t, state)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner); err == nil {
		t.Fatal("snapshot silently substituted current package")
	}
}

func TestNativeClaimUnavailableSnapshotFinalizesRunAndBilling(t *testing.T) {
	for _, mutation := range []string{"disabled", "disabled-pending", "uninstalled", "missing", "corrupt", "metadata", "collision", "storage-transient"} {
		t.Run(mutation, func(t *testing.T) {
			s, db := agentRunFixture(t)
			skill, err := s.CreateSkill("user", SkillMutationRequest{SkillName:"Native", Description:"For scripts", Instruction:"# Frozen", Tag:"others", IsPrivate:true})
			if err != nil { t.Fatal(err) }
			before, err := s.repo.CreditAccount("user"); if err != nil { t.Fatal(err) }
			req := agentTestRequest(); req.SkillIDs = []string{skill.SkillID}
			view, err := s.CreateCloudAgentRun("user", req, ""); if err != nil { t.Fatal(err) }
			run, state := reloadPiRun(t, s, view.ID)
			childID := ""
			if mutation == "disabled-pending" {
				run, state = startPiAgentFirstStep(t, s, view.ID)
				childID = state.ActiveTaskID
				if err := db.Model(run).Update("lease_expires_at", nil).Error; err != nil { t.Fatal(err) }
				if err := db.Model(&model.CloudAgentPiSession{}).Where("active_run_id = ?", run.ID).Update("lease_expires_at", nil).Error; err != nil { t.Fatal(err) }
			}
			if mutation == "collision" {
				state.Skills = append(state.Skills, state.Skills[0])
				if err := db.Model(run).Update("state_json", marshalNativeTestState(t, *state)).Error; err != nil { t.Fatal(err) }
			}
			switch mutation {
			case "disabled", "disabled-pending":
				err = db.Model(&model.Skill{}).Where("id = ?", skill.SkillID).Update("status", 0).Error
			case "uninstalled":
				err = db.Model(&model.Skill{}).Where("id = ?", skill.SkillID).Updates(map[string]any{"owner_id":"author", "is_private":false}).Error
				if err == nil { _, err = s.SetSkillAdded("user", skill.SkillID, false) }
			case "missing", "corrupt":
				version, readErr := s.repo.SkillVersion(skill.VersionID); if readErr != nil { t.Fatal(readErr) }
				filename := filepath.Join(s.dataDir, "skill-packages", filepath.FromSlash(version.PackageKey))
				if mutation == "missing" { err = os.Remove(filename) } else { err = os.WriteFile(filename, []byte("not a zip"), 0600) }
			case "metadata":
				err = db.Model(&model.SkillFile{}).Where("skill_version_id = ?", skill.VersionID).Update("sha256", "corrupted").Error
			case "storage-transient":
				err = db.Callback().Query().Before("gorm:query").Register("native-transient", func(tx *gorm.DB) {
					if tx.Statement.Table == "skill_versions" { tx.AddError(errors.New("temporary storage unavailable")) }
				})
			}
			if err != nil { t.Fatal(err) }
			snapshot, claimErr := s.ClaimPiAgent("worker-a")
			if snapshot != nil || claimErr == nil { t.Fatalf("invalid snapshot claim = %+v, %v", snapshot, claimErr) }
			latest, _ := reloadPiRun(t, s, view.ID)
			after, err := s.repo.CreditAccount("user"); if err != nil { t.Fatal(err) }
			if mutation == "storage-transient" {
				if latest.Status == "failed" || after.ReservedMicrocredits == 0 { t.Fatal("transient failure was finalized") }
				return
			}
			if latest.Status != "failed" || latest.CleanupPending || latest.ActiveTaskID != "" { t.Fatalf("snapshot failure stranded run: status=%s cleanup=%v active=%s", latest.Status, latest.CleanupPending, latest.ActiveTaskID) }
			if childID != "" {
				child, err := s.repo.TaskForUser("user", childID); if err != nil { t.Fatal(err) }
				if child.Status != model.TaskStatusCancelled { t.Fatalf("child task not cancelled: %s", child.Status) }
			}
			if after.ReservedMicrocredits != before.ReservedMicrocredits || after.AvailableMicrocredits != before.AvailableMicrocredits { t.Fatalf("holding reservation not refunded: before=%+v after=%+v", before, after) }
			var order model.BillingOrder
			if err := db.First(&order, "task_id = ?", state.PlaceholderTaskID).Error; err != nil { t.Fatal(err) }
			if order.Status != model.BillingStatusRefunded { t.Fatalf("holding order = %s", order.Status) }
			session, _, err := s.repo.CloudAgentPiSession("user", run.ConversationID); if err != nil { t.Fatal(err) }
			if session.LeaseOwner != "" { t.Fatal("failed claim retained session lease") }
			if snapshot, err := s.ClaimPiAgent("worker-b"); err != nil || snapshot != nil { t.Fatalf("terminal run reclaimed: %+v %v", snapshot, err) }
		})
	}
}

func TestNativeSkillReadsRejectRevokedInstallationAndDisabledSkills(t *testing.T) {
	for _, mutation := range []map[string]any{{"status": 0}, {"owner_id": "author", "is_private": false}} {
		s, db, run, frozen := nativeSkillLeasedFixture(t)
		if err := db.Model(&model.Skill{}).Where("id = ?", frozen.ID).Updates(mutation).Error; err != nil {
			t.Fatal(err)
		}
		if mutation["owner_id"] != nil {
			if _, err := s.SetSkillAdded("user", frozen.ID, false); err != nil {
				t.Fatal(err)
			}
		}
		request := PiSkillFileRequest{NativeName: frozen.NativeName, Path: "SKILL.md"}
		if _, err := s.PiSkillFile("user", run.ID, run.LeaseOwner, request); err == nil {
			t.Fatalf("read accepted revoked Skill: %v", mutation)
		}
		if _, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner); err == nil {
			t.Fatalf("restart accepted revoked Skill: %v", mutation)
		}
	}
}

func TestNativeReadCheckpointRequiresPairedCallAndRecordsSafeEvent(t *testing.T) {
	s, _, run, frozen := nativeSkillLeasedFixture(t)
	result := json.RawMessage(`{"role":"toolResult","toolCallId":"read-1","toolName":"read","content":[{"type":"text","text":"# Before update"}]}`)
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 1, Message: result}); err == nil {
		t.Fatal("unpaired native read result accepted")
	}
	call := map[string]any{"role": "assistant", "content": []map[string]any{{"type": "toolCall", "id": "read-1", "name": "read",
		"arguments": map[string]any{"path": "C:/temp/skills/" + frozen.NativeName + "/SKILL.md"}}}}
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 1, Message: encoded}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 2, Message: result}); err != nil {
		t.Fatal(err)
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 2, Message: result}); err != nil {
		t.Fatalf("replayed checkpoint failed: %v", err)
	}
	if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, PiMessageCheckpoint{Sequence: 3, Message: result}); err == nil {
		t.Fatal("duplicate result accepted")
	}
	view, err := s.CloudAgentRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(view)
	if !strings.Contains(string(data), "native_skill_read") || strings.Contains(string(data), "C:/temp/skills") {
		t.Fatalf("unsafe or missing native read event: %s", data)
	}
}

func TestNativeSkillPublicViewExposesModeButNotFilesOrBodies(t *testing.T) {
	s, _, run, _ := nativeSkillLeasedFixture(t)
	view, err := s.CloudAgentRun("user", run.ID)
	if err != nil { t.Fatal(err) }
	encoded, err := json.Marshal(view)
	if err != nil { t.Fatal(err) }
	text := string(encoded)
	if !strings.Contains(text, `"skillRuntimeMode":"pi-native"`) || strings.Contains(text, `"nativeFiles"`) || strings.Contains(text, "Before update") {
		t.Fatalf("invalid public view: %s", text)
	}
}

func TestNativeSkillRunEmitsEnablementAndRestoresFrozenSnapshotAfterUpdate(t *testing.T) {
    s, _ := agentRunFixture(t)
    skill, err := s.CreateSkill("user", SkillMutationRequest{SkillName: "Native", Description: "For scripts", Instruction: "# Frozen entry", Tag: "others", IsPrivate: true})
    if err != nil { t.Fatal(err) }
    request := agentTestRequest()
    request.SkillIDs = []string{skill.SkillID}
    view, err := s.CreateCloudAgentRun("user", request, "")
    if err != nil { t.Fatal(err) }
    if len(view.Events) == 0 || view.Events[0].Type != "native_skill_enabled" || view.Events[0].Payload["skillName"] != "Native" {
        t.Fatalf("missing native enablement: %+v", view.Events)
    }
    if _, err := s.UpdateSkill("user", skill.SkillID, SkillMutationRequest{SkillName: "Updated", Description: "New scripts", Instruction: "# New entry", Tag: "others", IsPrivate: true}); err != nil { t.Fatal(err) }
    snapshot, err := s.ClaimPiAgent("worker-a")
    if err != nil || snapshot == nil { t.Fatalf("claim: %v", err) }
    if snapshot.SkillRuntimeMode != "pi-native" || len(snapshot.Skills) != 1 || snapshot.Skills[0].VersionID != skill.VersionID || snapshot.Skills[0].ContentHash != skill.ContentHash || snapshot.Skills[0].DisplayName != "Native" {
        t.Fatalf("frozen identity changed: %+v", snapshot.Skills)
    }
    page, err := s.PiSkillFile("user", view.ID, "worker-a", PiSkillFileRequest{NativeName: snapshot.Skills[0].NativeName, Path: "SKILL.md"})
    if err != nil || !strings.Contains(page.Content, "# Frozen entry") || strings.Contains(page.Content, "# New entry") { t.Fatalf("frozen read: page=%+v err=%v", page, err) }
}

func TestNativeAssembledPromptSurvivesWorkerDirectoryChangeWithoutAllowingContentDrift(t *testing.T) {
    skill := cloudAgentSkill{ID: "one", NativeName: "skill-abc"}
    original := "SERVER POLICY\n<skills>\n<available_skills>\n<skill>\n<name>skill-abc</name>\n<description>Scripts</description>\n<location>C:\\temp\\canvas-pi-run-old\\cwd\\skills\\skill-abc\\SKILL.md</location>\n</skill>\n</available_skills>\n</skills>\n<cwd>\nC:/temp/canvas-pi-run-old/cwd\n</cwd>"
    restored := strings.ReplaceAll(strings.ReplaceAll(original, `C:\temp\canvas-pi-run-old\cwd\skills\skill-abc\SKILL.md`, "/tmp/canvas-pi-run-new/cwd/skills/skill-abc/SKILL.md"), "C:/temp/canvas-pi-run-old/cwd", "/tmp/canvas-pi-run-new/cwd")
    normalized := "SERVER POLICY\n<skills>\n<available_skills>\n<skill>\n<name>skill-abc</name>\n<description>Scripts</description>\n<location>skills/skill-abc/SKILL.md</location>\n</skill>\n</available_skills>\n</skills>\n<cwd>\ncanvas-agent-run\n</cwd>"
    state := cloudAgentRuntime{SkillRuntimeMode: cloudAgentSkillRuntimeNative, Skills: []cloudAgentSkill{skill},
        Snapshot: &cloudAgentContractSnapshot{AssembledPromptHash: cloudAgentTextDigest(normalized)}}
    if err := cloudAgentVerifyAssembledPrompt(&state, restored); err != nil { t.Fatalf("worker restart changed the prompt identity: %v", err) }
    for _, changed := range []string{strings.ReplaceAll(restored, "Scripts", "Changed"),
        strings.ReplaceAll(restored, "SERVER POLICY", "OTHER POLICY"), strings.ReplaceAll(restored, "/skills/skill-abc/", "/skills/other/")} {
        if err := cloudAgentVerifyAssembledPrompt(&state, changed); err == nil { t.Fatal("changed prompt content was accepted") }
    }
    state.SkillRuntimeMode = cloudAgentSkillRuntimeLegacy
    state.Snapshot.AssembledPromptHash = cloudAgentTextDigest(original)
    if err := cloudAgentVerifyAssembledPrompt(&state, restored); err == nil { t.Fatal("legacy prompt identity was weakened") }
}
