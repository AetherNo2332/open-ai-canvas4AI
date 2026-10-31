package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func storyboardSplitTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"id": "split-canvas", "referenceFieldsVersion": 1, "nodes": []map[string]any{
		{"id": "board", "type": "script", "title": "分镜", "position": map[string]any{"x": 0, "y": 0}, "metadata": map[string]any{
			"composerContent": "她在雨夜推门，与他对视后转身离开。",
			"storyboard": map[string]any{"rows": []any{
				map[string]any{"id": "a", "shotNumber": 1, "durationSeconds": 4, "plotDescription": "推门", "imageNodeId": "img-a"},
				map[string]any{"id": "b", "shotNumber": 2, "durationSeconds": 4, "plotDescription": "对视", "imageNodeId": "img-b"},
				map[string]any{"id": "c", "shotNumber": 3, "durationSeconds": 4, "plotDescription": "转身", "locked": true},
			}, "referenceNodeIds": []any{}},
		}},
		{"id": "style", "type": "text", "title": "项目风格", "position": map[string]any{"x": 0, "y": 400}, "metadata": map[string]any{"content": "冷色调，手持摄影"}},
		{"id": "img-a", "type": "image", "position": map[string]any{"x": 400, "y": 0}, "metadata": map[string]any{}},
		{"id": "img-b", "type": "image", "position": map[string]any{"x": 400, "y": 300}, "metadata": map[string]any{}},
	}, "connections": []map[string]any{{"id": "e-style", "fromNodeId": "style", "toNodeId": "board"}}}
	raw, _ := json.Marshal(doc)
	if err := db.Create(&model.CanvasProject{ID: "split-canvas", UserID: "user", PayloadJSON: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	return s, db
}

func splitCanvasRows(t *testing.T, s *Service) []map[string]any {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "split-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	_, board, rows, err := storyboardNodeFromDocument(doc, "board")
	if err != nil || board == nil {
		t.Fatalf("storyboard missing: %v", err)
	}
	return rows
}

// 拆镜脚本端到端：报价不改画布；局部重拆提交后，任务成功由服务端写回，只替换选中的行。
func TestSplitCanvasStoryboardResplitsRangeServerSide(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	config := map[string]any{"channelId": "channel", "model": "text-test"}
	quote, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", RowIDs: []string{"b"}, Request: "加一个她回头的反应", Config: config, QuoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if quote.TaskID != "" || quote.Quote.AmountMicrocredits <= 0 || !quote.Partial {
		t.Fatalf("quote must price without submitting: %+v", quote)
	}
	if !strings.Contains(quote.Prompt, "冷色调，手持摄影") || !strings.Contains(quote.Prompt, "加一个她回头的反应") || !strings.Contains(quote.Prompt, "【前一镜】") {
		t.Fatalf("prompt must be assembled from the canvas: %s", quote.Prompt)
	}
	if _, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", RowIDs: []string{"c"}, Config: config}); err == nil {
		t.Fatal("a locked row must not be re-split")
	}
	result, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", RowIDs: []string{"b"}, Request: "加一个她回头的反应", Config: config, QuoteHash: quote.Quote.QuoteHash, OperationID: "split-b"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID == "" {
		t.Fatal("submission must create a task")
	}
	var publicInput map[string]any
	if err := json.Unmarshal([]byte(publicTaskInputJSON(func() string {
		var task model.Task
		_ = db.Where("id = ?", result.TaskID).First(&task).Error
		return task.InputJSON
	}())), &publicInput); err != nil {
		t.Fatal(err)
	}
	metadata, _ := publicInput["metadata"].(map[string]any)
	if metadata["canvasConsumer"] != "storyboard_document" {
		t.Fatalf("missing stable storyboard consumer marker: %+v", metadata)
	}
	output, _ := json.Marshal(map[string]any{"text": `{"rows":[{"durationSeconds":2,"plotDescription":"她回头"},{"durationSeconds":2,"plotDescription":"两人对视"}]}`})
	if err := db.Model(&model.Task{}).Where("id = ?", result.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": string(output)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.EnqueueCanvasMaterialization(result.TaskID); err != nil {
		t.Fatal(err)
	}
	_, ts := s.CanvasCommandServices()
	if err := ts.Materialize(context.Background(), result.TaskID); err != nil {
		t.Fatal(err)
	}
	rows := splitCanvasRows(t, s)
	if len(rows) != 4 || stringValue(rows[0]["id"]) != "a" || stringValue(rows[0]["imageNodeId"]) != "img-a" {
		t.Fatalf("row before the range must not change: %+v", rows)
	}
	if rows[1]["plotDescription"] != "她回头" || rows[2]["plotDescription"] != "两人对视" || stringValue(rows[1]["imageNodeId"]) != "" {
		t.Fatalf("replaced range must hold the new rows without old media: %+v", rows)
	}
	if stringValue(rows[3]["id"]) != "c" || rows[3]["locked"] != true {
		t.Fatalf("locked row after the range must stay: %+v", rows[3])
	}
}

func TestSplitCanvasStoryboardAcceptsBrowserSystemConfigOnSubmit(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	config := map[string]any{
		"channelId": "channel", "channelModelKey": "text-test", "model": "text-test",
		"apiFormat": "openai", "interfaceType": "openai", "apiKey": "", "baseUrl": "", "secretKey": "",
		"capabilityConfig": map[string]any{"text": map[string]any{"maxOutputTokens": 4096}},
	}
	quoted, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", Config: config, QuoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.SplitCanvasStoryboard("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", Config: config, QuoteHash: quoted.Quote.QuoteHash, OperationID: "browser-config-submit"})
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID == "" {
		t.Fatal("browser-shaped system config must submit after quote")
	}
	var task model.Task
	if err := db.Where("id = ?", result.TaskID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(task.InputJSON, "apiKey") || strings.Contains(task.InputJSON, "baseUrl") || strings.Contains(task.InputJSON, "capabilityConfig") {
		t.Fatalf("unsafe browser config leaked into task input: %s", task.InputJSON)
	}
}

func TestSplitCanvasStoryboardDirectSubmitWithoutQuote(t *testing.T) {
	s, _ := storyboardSplitTestService(t)
	plan, err := s.prepareCanvasStoryboardSplit("user", "split-canvas", CanvasStoryboardSplitRequest{
		NodeID: "board", Config: map[string]any{"channelId": "channel", "channelModelKey": "text-test", "model": "text-test"}, OperationID: "direct-submit-no-quote",
	}, &taskAdmission{ID: "direct-split-task", MaxCharge: 1000000})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := plan.submit(s.repo, s)
	if err != nil {
		t.Fatal(err)
	}
	if handle.TaskID == "" {
		t.Fatal("direct storyboard submission must return a task")
	}
}

func TestSplitCanvasStoryboardQuoteGuardsAndIdempotency(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := CanvasStoryboardSplitRequest{NodeID: "board", RowIDs: []string{"b"}, Config: map[string]any{"channelId": "channel", "model": "text-test"}, OperationID: "quote-guard-submit"}
	if _, err := s.SplitCanvasStoryboard("user", "split-canvas", req); err == nil {
		t.Fatal("unconfirmed browser submission accepted")
	}
	quoteReq := req
	quoteReq.QuoteOnly = true
	quote, err := s.SplitCanvasStoryboard("user", "split-canvas", quoteReq)
	if err != nil {
		t.Fatal(err)
	}
	req.QuoteHash = quote.Quote.QuoteHash
	changed := req
	changed.Request = "different request"
	if _, err := s.SplitCanvasStoryboard("user", "split-canvas", changed); err == nil {
		t.Fatal("changed quote accepted")
	}
	plan, err := s.prepareCanvasStoryboardSplit("user", "split-canvas", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	expired, _ := s.signStoryboardQuote(plan.quoteKey, time.Now().Add(-time.Minute))
	bad := req
	bad.QuoteHash = expired
	if _, err := s.SplitCanvasStoryboard("user", "split-canvas", bad); err == nil {
		t.Fatal("expired quote accepted")
	}
	var count int64
	db.Model(&model.Task{}).Where("operation = ?", "storyboard").Count(&count)
	if count != 0 {
		t.Fatal("rejected quotes created tasks")
	}
	result, err := s.SplitCanvasStoryboard("user", "split-canvas", req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.SplitCanvasStoryboard("user", "split-canvas", req)
	if err != nil || replay.TaskID != result.TaskID {
		t.Fatalf("lost idempotent receipt: %v %+v", err, replay)
	}
	if _, err := s.SplitCanvasStoryboard("user", "split-canvas", changed); err == nil {
		t.Fatal("operation ID reused with changed input")
	}
	db.Model(&model.Task{}).Where("operation = ?", "storyboard").Count(&count)
	if count != 1 {
		t.Fatalf("replay created %d tasks", count)
	}
}
func TestSplitCanvasStoryboardPriceChangeRejectsQuote(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := CanvasStoryboardSplitRequest{NodeID: "board", Config: map[string]any{"channelId": "channel", "model": "text-test"}, QuoteOnly: true}
	quote, err := s.SplitCanvasStoryboard("user", "split-canvas", req)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ChannelModelPriceTier{}).Where("id = ?", "tier").Update("unit_price_microcredits", 200).Error; err != nil {
		t.Fatal(err)
	}
	req.QuoteOnly = false
	req.QuoteHash = quote.Quote.QuoteHash
	if _, err := s.SplitCanvasStoryboard("user", "split-canvas", req); err == nil {
		t.Fatal("price drift accepted")
	}
}
func TestSplitCanvasStoryboardWritebackErrorsAreTerminal(t *testing.T) {
	for _, edited := range []bool{false, true} {
		t.Run(fmt.Sprint(edited), func(t *testing.T) {
			s, db := storyboardSplitTestService(t)
			plan, err := s.prepareCanvasStoryboardSplit("user", "split-canvas", CanvasStoryboardSplitRequest{NodeID: "board", RowIDs: []string{"b"}, Config: map[string]any{"channelId": "channel", "model": "text-test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			handle, err := plan.submit(s.repo, s)
			if err != nil {
				t.Fatal(err)
			}
			output := `{"text":"not JSON"}`
			expected := "storyboard_apply_failed"
			if edited {
				canvas, _ := s.repo.CanvasProjectForUser("user", "split-canvas")
				doc, _ := creationDocument(canvas.PayloadJSON)
				_, _, rows, _ := storyboardNodeFromDocument(doc, "board")
				rows[1]["dialogue"] = "user edit"
				encoded, _ := json.Marshal(doc)
				if err := db.Model(&model.CanvasProject{}).Where("id = ?", "split-canvas").Update("payload_json", string(encoded)).Error; err != nil {
					t.Fatal(err)
				}
				expected = "storyboard_edited"
				output = `{"rows":[{"plotDescription":"new"}]}`
			}
			if err := db.Model(&model.Task{}).Where("id = ?", handle.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": output}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.repo.EnqueueCanvasMaterialization(handle.TaskID); err != nil {
				t.Fatal(err)
			}
			_, ts := s.CanvasCommandServices()
			if err := ts.Materialize(context.Background(), handle.TaskID); err != nil {
				t.Fatal(err)
			}
			job, err := s.repo.CanvasMaterialization(handle.TaskID)
			if err != nil || job.State != "done" || job.Outcome != expected {
				t.Fatalf("writeback did not end: %+v %v", job, err)
			}
			canvas, _ := s.repo.CanvasProjectForUser("user", "split-canvas")
			doc, _ := creationDocument(canvas.PayloadJSON)
			node, _, _, _ := storyboardNodeFromDocument(doc, "board")
			meta := cloudAgentNodeMetadata(node)
			if meta["status"] != "error" || stringValue(meta["errorDetails"]) == "" {
				t.Fatalf("missing visible error: %+v", meta)
			}
			if err := ts.Materialize(context.Background(), handle.TaskID); err != nil {
				t.Fatal("terminal invalid output retried", err)
			}
		})
	}
}
