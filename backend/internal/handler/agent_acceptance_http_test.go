package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/app"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func chainToolCallReply(id, name, arguments string) string {
	raw, _ := json.Marshal(arguments)
	return `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(raw) + `}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`
}

func chainTextReply(text string) string {
	return `{"choices":[{"message":{"role":"assistant","content":"` + text + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`
}

// B6-A: 目标提交、双业务派工、用户审批、自动报告、总控汇总与账单归属全部经
// 真实 HTTP 与后台运行器完成；测试主体只用公共入口。
func TestAgentAcceptanceHTTPSupervisorChain(t *testing.T) {
	// 确定性上游跑在本机：允许测试访问 127.0.0.1。
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/b6-chain.db?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}

	pngBytes := chainPNG()
	upstreamCalls := atomic.Int64{}
	supervisorStage := atomic.Int64{}
	// 确定性上游按请求内容响应：任意运行器/worker 交错下，同一阶段得到同一指令。
	textUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		var body struct {
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		conversation := ""
		for _, message := range body.Messages {
			conversation += fmt.Sprint(message.Content) + "\n"
		}
		toolNames := map[string]bool{}
		for _, tool := range body.Tools {
			toolNames[tool.Function.Name] = true
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case toolNames["delegate_work"]:
			if supervisorStage.CompareAndSwap(0, 1) {
				_, _ = w.Write([]byte(chainToolCallReply("call-del-drama", "delegate_work", `{"actionKey":"drama","definitionId":"drama.agent","application":"drama","spaceType":"drama_project","spaceId":"drama-chain","handoff":"梳理第一集分镜","budgetMicrocredits":300000}`)))
				return
			}
			if supervisorStage.CompareAndSwap(1, 2) {
				_, _ = w.Write([]byte(chainToolCallReply("call-del-ecom", "delegate_work", `{"actionKey":"ecom","definitionId":"ecommerce.agent","application":"ecommerce","spaceType":"workbench_batch","spaceId":"batch-chain","handoff":"一张白底主图","budgetMicrocredits":300000}`)))
				return
			}
			if toolNames["finish_work"] && strings.Contains(conversation, "子工作报告（completed）：任务") {
				_, _ = w.Write([]byte(chainToolCallReply("call-finish", "finish_work", `{"resultSummary":"分镜完成，主图已生成并交付。"}`)))
				return
			}
			_, _ = w.Write([]byte(chainTextReply("已派工给短剧与电商 Agent，等待结果。")))
		case toolNames["ecommerce_generate_image"]:
			if strings.Contains(conversation, "已提交生成任务") || strings.Contains(conversation, "等待用户批准") {
				_, _ = w.Write([]byte(chainTextReply("已提交生成任务。")))
				return
			}
			_, _ = w.Write([]byte(chainToolCallReply("call-img", "ecommerce_generate_image", `{"prompt":"白底主图","channelId":"platform-channel","model":"gpt-image-2","parameters":{"size":"1024x1024"},"index":0,"count":1}`)))
		case toolNames["report_work"] && toolNames["drama_summary"]:
			if strings.Contains(conversation, "已向总控报告：completed") {
				_, _ = w.Write([]byte(chainTextReply("短剧部分完成。")))
				return
			}
			_, _ = w.Write([]byte(chainToolCallReply("call-drama-report", "report_work", `{"status":"completed","summary":"第一集分镜已完成并自检。"}`)))
		default:
			_, _ = w.Write([]byte(chainTextReply("收到，继续等待。")))
		}
	}))
	t.Cleanup(textUpstream.Close)

	imageUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":"%s"}]}`, chainB64(pngBytes))
	}))
	t.Cleanup(imageUpstream.Close)

	profile := app.DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceOpenAIImage), "gpt-image-2")
	profile.Image.Size = app.ImageSizeConfig{Parameter: "size", Values: []string{"1024x1024"}, Default: "1024x1024"}
	profile.Image.Quality = app.ImageQualityConfig{Supported: false}
	profile.Image.Resolution = app.ImageResolutionConfig{Supported: false}
	capabilityJSON, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := app.CapabilitySpecFromModelCapabilityConfig(profile, "image")
	if err != nil {
		t.Fatal(err)
	}
	specJSON, _ := json.Marshal(spec)
	textCapabilityJSON, _ := json.Marshal(app.DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-model"))

	for _, row := range []any{
		&model.User{ID: "chain-a", Username: "chain-a", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.CreditAccount{UserID: "chain-a", AvailableMicrocredits: 10_000_000},
		&model.AuthSession{ID: "chain-session-a", UserID: "chain-a", TokenHash: auth.HashToken("chain-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.Project{ID: "drama-chain", UserID: "chain-a", Name: "短剧", Type: "short-drama", Status: model.ProjectStatusActive, Revision: 1},
		&model.DramaProduction{ProjectID: "drama-chain", UserID: "chain-a", Status: model.DramaStatusPaused, Stage: model.DramaStageProduce, BudgetMicrocredits: 5_000_000},
		&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"imageWorkbenchEnabled":true,"creditsEnabled":true,"taskCenterEnabled":true}`},
		&model.ModelChannel{ID: "text-channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "文本", BaseURL: textUpstream.URL + "/v1", APIKey: "k", APIFormat: "openai"},
		&model.ChannelModel{ID: "text-cm", ChannelID: "text-channel", ModelKey: "text-model", ProviderModelKey: "text-model", Capability: "text", Protocol: model.ChannelInterfaceChatCompletion, CapabilityConfigJSON: string(textCapabilityJSON), BillingMode: "token", UnitPriceMicrocredits: 10_000, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "text-tier", ChannelModelID: "text-cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "token", UnitPriceMicrocredits: 10_000, InputTokenPriceMicrocredits: 1_000, OutputTokenPriceMicrocredits: 1_000, PriceConfigured: true, Enabled: true},
		&model.ModelChannel{ID: "platform-channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "平台图片", BaseURL: imageUpstream.URL, APIKey: "k", APIFormat: "openai"},
		&model.ChannelModel{ID: "platform-cm", ChannelID: "platform-channel", ModelKey: "gpt-image-2", ProviderModelKey: "gpt-image-2", Capability: "image", Protocol: model.ChannelInterfaceOpenAIImage, CapabilityConfigJSON: string(capabilityJSON), BillingMode: "fixed_request", UnitPriceMicrocredits: 100_000, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "platform-tier", ChannelModelID: "platform-cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 100_000, PriceConfigured: true, Enabled: true},
		&model.LogicalModel{ID: "chain-logical-image", Code: "chain-image", Name: "链路图片", Capability: "image", Enabled: true, ActiveRevisionID: "chain-revision", PricePolicy: "channel"},
		&model.LogicalModelRevision{ID: "chain-revision", LogicalModelID: "chain-logical-image", Version: 1, CapabilitySpecJSON: string(specJSON), DefaultOptionsJSON: `{"size":"1024x1024"}`},
		&model.LogicalModelRoute{ID: "chain-route", LogicalModelRevisionID: "chain-revision", ChannelModelID: "platform-cm", Enabled: true, Weight: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := service.New(repository.New(db), t.TempDir())
	svc.StartWorker()
	t.Cleanup(svc.BeginDrain)
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	startChainPiWorker(t, router, svc)
	t.Cleanup(func() { writeChainPiEvidence(t, db) })

	call := func(method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
		raw := []byte(nil)
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "chain-session-a.chain-token"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		envelope := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &envelope)
		return w, envelope
	}
	decode := func(envelope map[string]any, target any) {
		raw, err := json.Marshal(envelope["data"])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatalf("decode %v: %v", envelope, err)
		}
	}

	// 1. 公共入口提交目标：根工作、总控会话与首轮同事务创建。
	w, envelope := call(http.MethodPost, "/api/agent/v2/work", map[string]any{
		"goal": "第一集分镜 + 一张主图", "budgetMicrocredits": 1_000_000, "idempotencyKey": "b6-chain", "sourceMessageId": "goal-msg",
		"initialMessage": "请派工完成第一集分镜和一张白底主图",
		"authorizedScopes": []map[string]any{
			{"application": "drama", "spaceType": "drama_project", "spaceId": "drama-chain"},
			{"application": "ecommerce", "spaceType": "workbench_batch", "spaceId": "batch-chain"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("start work: %d %s", w.Code, w.Body.String())
	}
	var work struct {
		ID                  string `json:"id"`
		SupervisorSessionID string `json:"supervisorSessionId"`
		BudgetGrantID       string `json:"budgetGrantId"`
		Status              string `json:"status"`
	}
	decode(envelope, &work)

	// 2. 等待运行器自动推进整条链路。
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		w, envelope = call(http.MethodGet, "/api/agent/v2/work/"+work.ID, nil)
		if w.Code == http.StatusOK {
			decode(envelope, &work)
			if work.Status == "completed" {
				break
			}
			// 生成报价达到审批阈值：用户经公共入口批准，运行器自动重放挂起的操作。
			w, envelope = call(http.MethodGet, "/api/agent/v2/work/"+work.ID+"/approvals", nil)
			if w.Code == http.StatusOK {
				var approvals struct {
					Approvals []struct {
						ID     string `json:"id"`
						Status string `json:"status"`
					} `json:"approvals"`
				}
				decode(envelope, &approvals)
				for _, approval := range approvals.Approvals {
					if approval.Status == "pending" {
						if dw, denvelope := call(http.MethodPost, "/api/agent/v2/approvals/"+approval.ID+"/decision", map[string]any{"decision": "approved", "expectedAuthorizationVersion": 1}); dw.Code != http.StatusOK {
							t.Fatalf("approval decision: %d %s", dw.Code, string(denvelope["msg"].(json.RawMessage)))
						}
					}
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if work.Status != "completed" {
		var turns []model.AgentTurn
		_ = db.Find(&turns).Error
		var outbox []model.AgentOutbox
		_ = db.Find(&outbox).Error
		var tasks []model.Task
		_ = db.Where("user_id = ?", "chain-a").Find(&tasks).Error
		details := []string{}
		for _, task := range tasks {
			details = append(details, fmt.Sprintf("task=%s type=%s status=%s stage=%s error=%s provider=%s", task.ID, task.Type, task.Status, task.Stage, task.Error, task.Provider))
		}
		turnStates := []string{}
		for _, row := range turns {
			turnStates = append(turnStates, row.SessionID[:8]+"/"+row.MessageID+"/"+row.Status+"/attempts="+fmt.Sprint(row.Attempts))
		}
		var assignments []model.AgentAssignment
		_ = db.Find(&assignments).Error
		assignmentStates := []string{}
		for _, row := range assignments {
			assignmentStates = append(assignmentStates, row.WorkItemID[:8]+"/"+row.ExecutorSessionID[:8]+"/"+row.Status)
		}
		var approvalsRows []model.AgentApproval
		_ = db.Find(&approvalsRows).Error
		var operations []model.AgentOperation
		_ = db.Find(&operations).Error
		opStates := []string{}
		for _, operation := range operations {
			opStates = append(opStates, operation.Name+":"+operation.Status+":"+operation.Status+":"+fmt.Sprint(operation.ApprovalID != ""))
		}
		approvalStates := []string{}
		for _, row := range approvalsRows {
			approvalStates = append(approvalStates, row.Status)
		}
		var messages []model.AgentSessionMessage
		_ = db.Where("owner_id = ?", "chain-a").Order("sequence ASC").Find(&messages).Error
		history := []string{}
		for _, row := range messages {
			history = append(history, row.SessionID[:8]+"/"+row.Kind+"/"+row.Role+":"+truncateChain(row.Body, 160))
		}
		t.Fatalf("chain did not complete in time; status %s, upstream calls %d, turns=%d outbox=%d details=%v approvals=%v operations=%v turnRows=%v assignments=%v | history=%s", work.Status, upstreamCalls.Load(), len(turns), len(outbox), details, approvalStates, opStates, turnStates, assignmentStates, strings.Join(history, " || "))
	}

	// 3. 公共入口核对工作树、执行会话与账单归属。
	w, envelope = call(http.MethodGet, "/api/agent/v2/works?limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("works directory: %d", w.Code)
	}
	var directory struct {
		Works []struct {
			Work struct {
				ID            string `json:"id"`
				Status        string `json:"status"`
				ResultSummary string `json:"resultSummary"`
			} `json:"work"`
			Children []struct {
				Work struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"work"`
				ExecutorSessionID string `json:"executorSessionId"`
			} `json:"children"`
		} `json:"works"`
	}
	decode(envelope, &directory)
	if len(directory.Works) != 1 || len(directory.Works[0].Children) != 2 {
		t.Fatalf("work directory: %+v", directory)
	}
	for _, child := range directory.Works[0].Children {
		if child.Work.Status != "completed" || child.ExecutorSessionID == "" {
			t.Fatalf("child not completed or without an executor session: %+v", child)
		}
	}
	var orders, charges, mismatch int64
	if err := db.Model(&model.BillingOrder{}).Where("user_id = ?", "chain-a").Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.PlatformBudgetCharge{}).Where("owner_id = ?", "chain-a").Count(&charges).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.PlatformBudgetCharge{}).Where("owner_id = ? AND root_id <> ?", "chain-a", work.BudgetGrantID).Count(&mismatch).Error; err != nil {
		t.Fatal(err)
	}
	if orders == 0 || orders != charges || mismatch != 0 {
		t.Fatalf("billing: orders=%d charges=%d outside-root=%d", orders, charges, mismatch)
	}

	// 4. 同幂等键重放目标返回同一工作。
	w, envelope = call(http.MethodPost, "/api/agent/v2/work", map[string]any{
		"goal": "第一集分镜 + 一张主图", "budgetMicrocredits": 1_000_000, "idempotencyKey": "b6-chain",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("replayed start: %d", w.Code)
	}
	var replayed struct {
		ID string `json:"id"`
	}
	decode(envelope, &replayed)
	if replayed.ID != work.ID {
		t.Fatalf("replay created a new work: %s vs %s", replayed.ID, work.ID)
	}
	// 总控与业务页面共享同一份报告目录：两份完成报告在根工作可见。
	w, envelope = call(http.MethodGet, "/api/agent/v2/work/"+work.ID+"/reports", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("reports: %d", w.Code)
	}
	var reports struct {
		Reports []struct {
			Status  string `json:"status"`
			Applied bool   `json:"applied"`
		} `json:"reports"`
	}
	decode(envelope, &reports)
	completed := 0
	for _, report := range reports.Reports {
		if report.Status == "completed" && report.Applied {
			completed++
		}
	}
	if completed < 2 {
		t.Fatalf("completed applied reports = %d in %+v", completed, reports)
	}
	_ = strings.TrimSpace
}

func truncateChain(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func chainPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	out := &strings.Builder{}
	if err := png.Encode(chainWriter{out}, img); err != nil {
		panic(err)
	}
	return []byte(out.String())
}

type chainWriter struct{ out *strings.Builder }

func (w chainWriter) Write(p []byte) (int, error) { return w.out.Write(p) }

func chainB64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
