package observability

import (
	"reflect"
	"testing"
)

func TestEstimateDiagnosticPreview(t *testing.T) {
	empty := EstimateDiagnosticPreview(0, 0, 0, 0, false)
	if empty.ClientEventLimit != DiagnosticMaxClientEvents || empty.TaskCount != 0 || empty.TaskLogCount != 0 || empty.APICallCount != 0 || empty.EstimatedBytes != 2048 || empty.WillTruncate {
		t.Fatalf("EstimateDiagnosticPreview(empty) = %#v", empty)
	}

	got := EstimateDiagnosticPreview(10, 2, 3, 4, true)
	wantBytes := int64(2048 + 10*420 + 2*620 + 3*700 + 4*520)
	if got.ClientEventLimit != 500 || got.TaskCount != 2 || got.TaskLogCount != 3 || got.APICallCount != 4 || got.EstimatedBytes != wantBytes || !got.WillTruncate {
		t.Fatalf("EstimateDiagnosticPreview() = %#v", got)
	}
}

func TestTailWithinLimitPreservesNilAndEmptySlices(t *testing.T) {
	var nilItems []int
	got, truncated := TailWithinLimit(nilItems, 2)
	if got != nil || truncated {
		t.Fatalf("TailWithinLimit(nil) = %#v, %v", got, truncated)
	}

	emptyItems := []int{}
	got, truncated = TailWithinLimit(emptyItems, 2)
	if got == nil || len(got) != 0 || truncated {
		t.Fatalf("TailWithinLimit(empty) = %#v, %v", got, truncated)
	}

	within := []int{1, 2}
	got, truncated = TailWithinLimit(within, 2)
	if !reflect.DeepEqual(got, within) || truncated {
		t.Fatalf("TailWithinLimit(within) = %#v, %v", got, truncated)
	}

	over := []int{1, 2, 3, 4}
	got, truncated = TailWithinLimit(over, 2)
	if !reflect.DeepEqual(got, []int{3, 4}) || !truncated {
		t.Fatalf("TailWithinLimit(over) = %#v, %v", got, truncated)
	}

	got, truncated = TailWithinLimit(over, 0)
	if len(got) != 0 || !truncated {
		t.Fatalf("TailWithinLimit(zero) = %#v, %v", got, truncated)
	}

	got, truncated = TailWithinLimit(over, -1)
	if len(got) != 0 || !truncated {
		t.Fatalf("TailWithinLimit(negative) = %#v, %v", got, truncated)
	}
}
