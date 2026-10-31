package app

import (
	"strings"
	"testing"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCloudAgentStoryboardAsyncSubmitWaitAndNotice(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks = 2
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("split-call", "storyboard", `{"action":"resplit","nodeId":"board","rowIds":["b"],"request":"延长对视","channelId":"channel","channelModelKey":"text-test"}`)
	wait := piAgentTestCall("wait-call", "wait_generations", `{}`)
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call, wait})
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 1 || state.CallIndex != 1 {
		t.Fatalf("split must return immediately: %+v %d", state.PendingGenerations, state.CallIndex)
	}
	taskID := state.PendingGenerations[0].TaskID
	task, _ := s.repo.TaskForUser("user", taskID)
	if task.AgentRunID != run.ID || !cloudAgentContainsString(state.TaskIDs, taskID) {
		t.Fatal("split bypassed run admission")
	}
	before, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range before.PendingInterjections {
		if item.Source == cloudAgentGenerationSource {
			t.Fatal("completion notice before writeback")
		}
	}
	receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, wait.ID)
	if err != nil || !receipt.Suspended {
		t.Fatalf("wait did not suspend: %+v %v", receipt, err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"rows":[{"plotDescription":"新的对视","durationSeconds":5}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if settled, err := s.SettlePendingCloudAgentGenerations(); err != nil || settled != 1 {
		t.Fatalf("settle=%d %v", settled, err)
	}
	run, state = agentInterjectionState(t, s, run.ID)
	if run.WaitKind != "" || len(state.PendingGenerations) != 0 || state.CallIndex != 2 {
		t.Fatalf("same run failed to resume: %s %d", run.WaitKind, state.CallIndex)
	}
	message := cloudAgentToolMessageForTest(t, state, wait.ID)
	if !strings.Contains(message, "splitResult") || !strings.Contains(message, `"rowId":"b"`) {
		t.Fatalf("wait receipt lost mapping: %s", message)
	}
	task, _ = s.repo.TaskForUser("user", taskID)
	if facts := cloudAgentTaskDiagnostic(s.repo, task); facts["splitResult"] == nil {
		t.Fatal("task_get lost mapping")
	}
	rows := splitCanvasRows(t, s)
	if rows[1]["id"] != "b" || rows[1]["mediaReview"] != "pending" || rows[2]["locked"] != true {
		t.Fatalf("wrong resplit rows: %+v", rows)
	}
}

func TestCloudAgentStoryboardLockedWrites(t *testing.T) {
	s, _ := storyboardSplitTestService(t)
	for _, raw := range []string{
		`{"action":"update","nodeId":"board","rowIds":["a","c"],"patch":{"dialogue":"change"}}`,
		`{"action":"update","nodeId":"board","updates":[{"rowId":"c","patch":{"dialogue":"change"}}]}`,
		`{"action":"set_assets","nodeId":"board","rowId":"c","assets":[]}`,
		`{"action":"set_references","nodeId":"board","references":[]}`,
		`{"action":"bind_output","nodeId":"board","rowId":"c","outputMode":"image","outputNodeId":"img-a"}`,
	} {
		call := piAgentTestCall("locked", "storyboard", raw)
		fields, action, err := storyboardArguments(call)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepareStoryboardWriteCall(s.repo, "user", "split-canvas", call, fields, action); err == nil || !strings.Contains(err.Error(), "已锁定") {
			t.Fatalf("locked %s accepted: %v", action, err)
		}
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", "split-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	call := piAgentTestCall("unlock", "storyboard", `{"action":"unlock","nodeId":"board","rowIds":["c"]}`)
	fields, action, _ := storyboardArguments(call)
	fields["snapshotHash"] = cloudAgentCanvasHash(doc)
	internal, err := prepareStoryboardWriteCall(s.repo, "user", "split-canvas", call, fields, action)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", "split-canvas", internal, policy); err != nil {
		t.Fatal(err)
	}
	read := piAgentTestCall("read", "storyboard", `{"nodeId":"board"}`)
	result, err := cloudAgentReadStoryboard(s.repo, "user", "split-canvas", read)
	if err != nil {
		t.Fatal(err)
	}
	if markdown := cloudAgentStoryboardMarkdown(result.(map[string]any)); !strings.Contains(markdown, "| b |") || !strings.Contains(markdown, "待复核") {
		t.Fatalf("missing readable table: %s", markdown)
	}
}
func TestCloudAgentStoryboardApprovalAndBudget(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget", true: "approval"}[approved], func(t *testing.T) {
			s, db := storyboardSplitTestService(t)
			req := agentTestRequest()
			req.CanvasID = "split-canvas"
			req.PermissionMode = "auto"
			if approved {
				req.PermissionMode = "request_approval"
			}
			root, err := s.CreateCloudAgentRun("user", req, "")
			if err != nil {
				t.Fatal(err)
			}
			call := piAgentTestCall("split-call", "storyboard", `{"action":"split","nodeId":"board","channelId":"channel","channelModelKey":"text-test"}`)
			run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
			if !approved {
				orders, err := s.repo.BillingOrdersByTaskIDs("user", state.TaskIDs)
				if err != nil {
					t.Fatal(err)
				}
				var spent int64
				for _, order := range orders {
					spent += order.AmountMicrocredits
				}
				state.Request.Budget.MaxCredits = float64(spent+50) / float64(CreditScale)
				if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
					return cloudAgentSave(current, &state)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
				t.Fatal(err)
			}
			run, state = agentInterjectionState(t, s, run.ID)
			var count int64
			db.Model(&model.Task{}).Where("operation = ?", "storyboard").Count(&count)
			if count != 0 {
				t.Fatal("task billed before approval or over budget")
			}
			if !approved {
				if len(state.PendingGenerations) != 0 || !strings.Contains(cloudAgentToolMessageForTest(t, state, call.ID), "积分上限") {
					t.Fatal("budget refusal not returned to model")
				}
				return
			}
			if run.Status != "waiting_approval" || state.Approval == nil || state.Approval.QuoteHash == "" {
				t.Fatalf("missing approval: %s %+v", run.Status, state.Approval)
			}
			if err := s.DecideCloudAgentApproval("user", run.ID, state.Approval.ID, "approve", ""); err != nil {
				t.Fatal(err)
			}
			if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
				t.Fatal(err)
			}
			_, state = agentInterjectionState(t, s, run.ID)
			if len(state.PendingGenerations) != 1 {
				t.Fatal("approved split not submitted")
			}
		})
	}
}
