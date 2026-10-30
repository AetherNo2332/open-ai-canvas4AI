package app

import (
	"errors"
	"testing"
)

// 上游 #917：Pi 直接接收工具结果，本批末项不是看图（成功或失败）时也必须补送已查看的图片。

func TestCloudAgentPiMixedBatchFlushesImages(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "failed_final_tool"
		}
		t.Run(name, func(t *testing.T) {
			state := cloudAgentPairingState(
				cloudAgentPairingCall("image", "canvas_inspect_image", "{}"),
				cloudAgentPairingCall("state", "canvas_get_state", "{}"),
			)
			cloudAgentToolResult("run", state, state.Calls[0], cloudAgentPairingInspection("image-a", "https://example.test/a"), nil)
			if len(state.PendingImageInspections) != 1 || len(cloudAgentImageMessageIndexes(state.Canonical.Messages)) != 0 {
				t.Fatal("image must remain buffered until all tool results arrive")
			}
			var toolErr error
			if failed {
				toolErr = errors.New("read failed")
			}
			cloudAgentToolResult("run", state, state.Calls[1], map[string]any{"ok": true}, toolErr)
			// Pi does not call advanceCloudAgent's fallback flush.
			assertPiBatchImages(t, state, 1)
			if cloudAgentFlushPendingImages(state) {
				t.Fatal("batch completion must not leave a duplicate image to flush")
			}
		})
	}
}

func assertPiBatchImages(t *testing.T, state *cloudAgentRuntime, count int) {
	t.Helper()
	if len(state.PendingImageInspections) != 0 {
		t.Fatalf("Pi batch left %d images pending", len(state.PendingImageInspections))
	}
	assertCloudAgentToolCallPairing(t, state.Canonical.Messages)
	indexes := cloudAgentImageMessageIndexes(state.Canonical.Messages)
	if len(indexes) != 1 || indexes[0] != len(state.Canonical.Messages)-1 {
		t.Fatalf("expected one trailing image message: %s", cloudAgentDebugMessages(state.Canonical.Messages))
	}
	if got := cloudAgentMessageImagePartCount(state.Canonical.Messages[indexes[0]]); got != count {
		t.Fatalf("image count=%d, want %d", got, count)
	}
}
