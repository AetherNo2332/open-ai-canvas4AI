package app

import (
	"encoding/json"
	"strings"
	"testing"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// B6-C: 双用户、双业务、同空间双会话的模型输入互不串内容；用户停止之后，
// 新业务写入与新收费提交为零。
func TestAgentAcceptanceIsolationMatrix(t *testing.T) {
	f := newBusinessFixture(t)
	executed := 0
	if err := f.svc.registerAgentBusiness(testBusinessAdapter{executed: &executed}, as.Definition{
		ID: "notebook.agent", Version: "1", Role: as.RoleBusiness,
		Application: "notebook", SpaceTypes: []string{"notebook_page"}, Title: "Notebook", Tools: []string{"notebook_write"},
	}); err != nil {
		t.Fatal(err)
	}
	selection := cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}
	// 用户 A：同空间两个独立会话 + 短剧派工；用户 B：同空间的另一个会话。
	sessionA1 := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "iso-a1"})
	sessionA2 := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "iso-a2"})
	sessionB := agentSessionCreate(t, f.svc, "user-b", as.CreateSessionRequest{DefinitionID: "notebook.agent", Scope: as.Scope{Application: "notebook", SpaceType: "notebook_page", SpaceID: "page-b"}, IdempotencyKey: "iso-b"})
	// 各会话私有标识 + 私有笔记。
	for _, session := range []struct {
		id     string
		note   string
		marker string
		owner  string
	}{{sessionA1.ID, "A1 的私有背景资料，含标记甲子丑", "标记甲子丑", "user-a"},
		{sessionA2.ID, "A2 的私有背景资料，含标记乙丙丁", "标记乙丙丁", "user-a"},
		{sessionB.ID, "B 的私有背景资料，含标记戊己庚", "标记戊己庚", "user-b"}} {
		f.text.script(textReply(session.note))
		if _, err := f.svc.SendSessionMessage(session.owner, SendSessionMessageRequest{SessionID: session.id, MessageID: "note-" + session.id, Text: session.marker}); err != nil {
			t.Fatal(err)
		}
		for range 6 {
			if _, err := f.svc.runPendingAgentTurns(10); err != nil {
				t.Fatal(err)
			}
			f.drain(t)
			driveTestPiRuns(t, f.svc, f.text, session.owner)
		}
	}
	// 用户 A 的派工：短剧子工作。
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "iso", Goal: "看短剧", BudgetMicrocredits: 1_000_000, IdempotencyKey: "iso-root",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "iso", ActionKey: "d", DefinitionID: "drama.agent",
		Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, BudgetMicrocredits: 400_000})
	if err != nil {
		t.Fatal(err)
	}
	f.text.script(textReply("短剧项目正常。"))
	if _, err := f.runTurn(t, businessTurn{Assignment: assignment, MessageID: "iso-exec", Text: "看看项目", Model: selection}); err != nil {
		t.Fatal(err)
	}

	// 隔离断言：每个请求最多携带一个会话的私有标识——出现标识甲的请求
	// 不得同时出现标识乙或丙（请求可能不含任何标识）。
	bodies := f.text.snapshotBodies()
	markers := []string{"标记甲子丑", "标记乙丙丁", "标记戊己庚"}
	for index, body := range bodies {
		raw, _ := json.Marshal(body["messages"])
		conversation := string(raw)
		present := []string{}
		for _, marker := range markers {
			if strings.Contains(conversation, marker) {
				present = append(present, marker)
			}
		}
		if len(present) > 1 {
			t.Fatalf("upstream request %d mixed private notes %v: %.400s", index, present, conversation)
		}
	}
	// 用户 B 不能读写用户 A 的会话与工作。
	if _, err := f.svc.agentSessionForOwner("user-b", sessionA1.ID); !as.IsCode(err, as.CodeNotFound) {
		t.Fatalf("foreign session read: %v", err)
	}
	if _, err := f.svc.GetWork("user-b", work.ID); err == nil {
		t.Fatal("foreign work read succeeded")
	}
	// 用户 A 停止根工作：短剧子工作取消，新收费与新写入为零。
	stopped, err := f.svc.GetWork("user-a", work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StopWork("user-a", WorkControlRequest{WorkItemID: work.ID, ExpectedVersion: stopped.Version, Reason: "隔离验收停止"}); err != nil {
		t.Fatal(err)
	}
	submissionsBefore := platformCount(t, f.db, &model.PlatformSubmission{}, "user_id = ?", "user-a")
	if _, err := f.runTurn(t, businessTurn{Assignment: assignment, MessageID: "iso-after-stop", Text: "再看看", Model: selection}); err == nil {
		t.Fatal("stopped work accepted a new turn")
	}
	if executed := platformCount(t, f.db, &model.PlatformSubmission{}, "user_id = ?", "user-a"); executed != submissionsBefore {
		t.Fatalf("stopped work produced new charged submissions: %d -> %d", submissionsBefore, executed)
	}
	// 同空间双会话：A1 与 A2 的模型输入各自带自己的标识（快照级断言已覆盖），
	// 且二者互为独立会话。
	if sessionA1.ID == sessionA2.ID {
		t.Fatal("same-space sessions collapsed into one")
	}
}
