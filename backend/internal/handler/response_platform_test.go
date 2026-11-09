package handler

import (
	"fmt"
	"testing"

	cp "yingce/backend/internal/creationplatform"
)

func TestFailServicePreservesPlatformRejections(t *testing.T) {
	for _, code := range []cp.ErrorCode{cp.CodeInsufficientCredits, cp.CodeIdempotencyConflict, cp.CodeModelUnavailable} {
		t.Run(string(code), func(t *testing.T) {
			recorder, ctx := responseTestContext()
			err := cp.NewError(code, "平台拒绝请求")
			failService(ctx, fmt.Errorf("workbench: %w", err))
			body := decodeFailureEnvelope(t, recorder)
			if recorder.Code != err.HTTPStatus() || body.Reason != string(code) || body.Msg != err.Message {
				t.Fatalf("status=%d body=%+v", recorder.Code, body)
			}
		})
	}
}
