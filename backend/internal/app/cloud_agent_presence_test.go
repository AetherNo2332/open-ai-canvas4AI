package app

import (
	"reflect"
	"sort"
	"testing"
)

func TestCloudAgentPresenceNodeIDsAndPhase(t *testing.T) {
	call := cloudAgentCall{}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = `{"snapshotHash":"h","ops":[{"type":"update_node","id":"a","patch":{"title":"x"}},{"type":"duplicate_node","id":"copy","sourceNodeId":"b"},{"type":"reorder_nodes","id":"g","nodeIds":["c","d"]}]}`
	ids := cloudAgentPresenceNodeIDs(call)
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"a", "b", "c", "copy", "d", "g"}) {
		t.Fatalf("ids = %v", ids)
	}
	if cloudAgentPresencePhase("canvas_apply_ops") != "editing" || cloudAgentPresencePhase("canvas_read_content") != "reading" || cloudAgentPresencePhase("web_search") != "" || cloudAgentPresencePhase("plan_update") != "" {
		t.Fatal("unexpected presence phase mapping")
	}
	call.Function.Arguments = `not json`
	if len(cloudAgentPresenceNodeIDs(call)) != 0 {
		t.Fatal("invalid arguments must not yield ids")
	}
}
