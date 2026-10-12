package observability

import "testing"

func TestExporterDropsWhenBoundedQueueIsFull(t *testing.T) {
	e := NewExporter(1, "")
	defer e.Close()
	_ = e.Emit(Event{TaskID: "a"})
	_ = e.Emit(Event{TaskID: "b"})
	_ = e.Emit(Event{TaskID: "c"})
	if e.Dropped() == 0 {
		t.Fatal("expected bounded exporter to drop low priority events")
	}
}
