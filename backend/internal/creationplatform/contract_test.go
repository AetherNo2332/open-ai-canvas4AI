package creationplatform

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 合同样例同时被 web/src/sdk/creation-platform 的测试读取，两端必须对同一份 JSON 得出同样结论。
func loadSample(t *testing.T, name string, target any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestContractSamplesCoverFourCapabilitiesAndBothModelRoutes(t *testing.T) {
	capabilities := map[Capability]bool{}
	routes := map[string]bool{}
	for _, name := range []string{"request-text-logical.json", "request-image-system.json", "request-video-system.json", "request-audio-logical.json"} {
		var req GenerationRequest
		loadSample(t, name, &req)
		if err := req.Validate(); err != nil {
			t.Fatalf("%s should be valid: %v", name, err)
		}
		capabilities[req.Capability] = true
		if req.Model.LogicalModelID != "" {
			routes["logical"] = true
		} else {
			routes["system"] = true
		}
	}
	for _, capability := range []Capability{CapabilityText, CapabilityImage, CapabilityVideo, CapabilityAudio} {
		if !capabilities[capability] {
			t.Errorf("no sample for capability %s", capability)
		}
	}
	if !routes["logical"] || !routes["system"] {
		t.Fatalf("samples must cover both model routes: %v", routes)
	}
}

func TestValidateRejectsAmbiguousOrIncompleteRequests(t *testing.T) {
	var base GenerationRequest
	loadSample(t, "request-image-system.json", &base)
	cases := map[string]func(*GenerationRequest){
		"both model routes":     func(r *GenerationRequest) { r.Model.LogicalModelID = "lm-1" },
		"no model route":        func(r *GenerationRequest) { r.Model = ModelSelection{} },
		"half system route":     func(r *GenerationRequest) { r.Model.ModelKey = "" },
		"unknown capability":    func(r *GenerationRequest) { r.Capability = "previs" },
		"wrong version":         func(r *GenerationRequest) { r.ContractVersion = 2 },
		"missing idempotency":   func(r *GenerationRequest) { r.IdempotencyKey = " " },
		"missing application":   func(r *GenerationRequest) { r.Business.Application = "" },
		"missing prompt":        func(r *GenerationRequest) { r.Prompt = "" },
		"negative cost cap":     func(r *GenerationRequest) { r.Cost.MaxMicrocredits = ptrInt64(-1) },
		"reference without id":  func(r *GenerationRequest) { r.References = []ReferenceResource{{Kind: "image"}} },
		"reference kind":        func(r *GenerationRequest) { r.References = []ReferenceResource{{ResourceID: "res-1", Kind: "file"}} },
		"oversized idempotency": func(r *GenerationRequest) { r.IdempotencyKey = strings.Repeat("k", 161) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := base.clone()
			mutate(&req)
			err := req.Validate()
			var platformErr *Error
			if !errors.As(err, &platformErr) || platformErr.Code != CodeInvalidRequest {
				t.Fatalf("Validate() = %v, want %s", err, CodeInvalidRequest)
			}
		})
	}
}

// 合同不能出现可以携带密钥、上游地址或业务专属目标的字段；新增此类字段即应在这里失败。
func TestContractTypesCarryNoSecretOrBusinessSpecificFields(t *testing.T) {
	forbidden := []string{"apikey", "secret", "baseurl", "header", "password", "token", "cookie", "credential", "interfacetype", "nodeid", "shotid", "rowid", "segmentid", "productid", "canvasid", "episodeid"}
	allowed := map[string]bool{"inputtokens": true, "outputtokens": true}
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			name := strings.ToLower(strings.Split(field.Tag.Get("json"), ",")[0])
			if name == "" {
				name = strings.ToLower(field.Name)
			}
			if !allowed[name] {
				for _, word := range forbidden {
					if strings.Contains(name, word) {
						t.Errorf("%s.%s matches forbidden %q", path, field.Name, word)
					}
				}
			}
			walk(field.Type, path+"."+field.Name)
		}
	}
	for _, value := range []any{GenerationRequest{}, Quote{}, TaskSnapshot{}, GenerationResult{}, TaskEvent{}, EventPage{}, Error{}, BillingSummary{}, ModelCatalog{}} {
		walk(reflect.TypeOf(value), reflect.TypeOf(value).Name())
	}
}

func TestFingerprintIgnoresTraceAndTracksContent(t *testing.T) {
	var req GenerationRequest
	loadSample(t, "request-image-system.json", &req)
	base := req.Fingerprint()
	traced := req.clone()
	traced.TraceID = "trace-other"
	if traced.Fingerprint() != base {
		t.Fatal("trace id must not change the idempotency fingerprint")
	}
	reordered := req.clone()
	reordered.Parameters = map[string]any{}
	for key, value := range req.Parameters {
		reordered.Parameters[key] = value
	}
	if reordered.Fingerprint() != base {
		t.Fatal("parameter map order must not change the fingerprint")
	}
	for name, mutate := range map[string]func(*GenerationRequest){
		"prompt": func(r *GenerationRequest) { r.Prompt += "!" },
		"model":  func(r *GenerationRequest) { r.Model.ModelKey = "other" },
		"reference": func(r *GenerationRequest) {
			r.References = append(r.References, ReferenceResource{ResourceID: "res-9", Kind: "image"})
		},
		"parameter": func(r *GenerationRequest) { r.Parameters["size"] = "16:9" },
		"cost cap":  func(r *GenerationRequest) { r.Cost.MaxMicrocredits = ptrInt64(1) },
		"business":  func(r *GenerationRequest) { r.Business.ObjectID = "other" },
	} {
		changed := req.clone()
		mutate(&changed)
		if changed.Fingerprint() == base {
			t.Errorf("%s change must change the fingerprint", name)
		}
	}
}

func TestSnapshotSamplesKeepExecutionResourceAndBillingStatesSeparate(t *testing.T) {
	var recovering TaskSnapshot
	loadSample(t, "snapshot-media-recovering.json", &recovering)
	if recovering.Execution != ExecutionSucceeded || recovering.Resources != ResourcesFailed || !recovering.Allows(ActionRecoverMedia) || recovering.Allows(ActionRetry) {
		t.Fatalf("upstream success with failed delivery must offer media recovery only: %+v", recovering)
	}
	var pending TaskSnapshot
	loadSample(t, "snapshot-billing-pending.json", &pending)
	if pending.Billing.Status != BillingPendingReview || pending.Billing.ChargedMicrocredits != nil {
		t.Fatalf("pending review must keep charged unknown: %+v", pending.Billing)
	}
	if pending.Allows(ActionRetry) {
		t.Fatal("pending billing review must not allow retry")
	}
	var succeeded TaskSnapshot
	loadSample(t, "snapshot-image-succeeded.json", &succeeded)
	if succeeded.Result == nil || len(succeeded.Result.Resources) != 1 || succeeded.Result.Resources[0].ResourceID == "" || succeeded.Billing.ChargedMicrocredits == nil {
		t.Fatalf("succeeded snapshot needs a stable resource id and settled charge: %+v", succeeded)
	}
}

func TestEventAndErrorSamplesDecode(t *testing.T) {
	var page EventPage
	loadSample(t, "events-page.json", &page)
	if len(page.Events) == 0 || page.NextCursor == "" {
		t.Fatalf("event page sample incomplete: %+v", page)
	}
	for index, event := range page.Events {
		if event.ID == 0 || event.TaskID == "" || event.Revision == 0 || event.Type == "" {
			t.Fatalf("event %d incomplete: %+v", index, event)
		}
		if index > 0 && event.ID <= page.Events[index-1].ID {
			t.Fatal("event ids must increase")
		}
	}
	var conflict Error
	loadSample(t, "error-idempotency-conflict.json", &conflict)
	if conflict.Code != CodeIdempotencyConflict || conflict.Retryable {
		t.Fatalf("idempotency conflict sample: %+v", conflict)
	}
}

func ptrInt64(value int64) *int64 { return &value }
