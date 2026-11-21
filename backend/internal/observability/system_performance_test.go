package observability

import (
	"encoding/json"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"
)

func TestMemoryMetricsJSONFields(t *testing.T) {
	withoutGC := ProjectMemory(runtime.MemStats{}, 0, 0, false)
	if got := memoryJSONKeys(t, withoutGC); !reflect.DeepEqual(got, []string{
		"availableBytes", "gcCount", "heapAllocBytes", "heapObjects", "heapSysBytes", "sysBytes", "systemAvailable", "totalBytes", "usagePercent", "usedBytes",
	}) {
		t.Fatalf("memory JSON keys without GC = %#v", got)
	}
	withGC := ProjectMemory(runtime.MemStats{LastGC: 1}, 0, 0, false)
	if got := memoryJSONKeys(t, withGC); !reflect.DeepEqual(got, []string{
		"availableBytes", "gcCount", "heapAllocBytes", "heapObjects", "heapSysBytes", "lastGCAt", "sysBytes", "systemAvailable", "totalBytes", "usagePercent", "usedBytes",
	}) {
		t.Fatalf("memory JSON keys with GC = %#v", got)
	}
}

func TestProjectMemory(t *testing.T) {
	lastGC := uint64(123456789)
	stats := runtime.MemStats{HeapAlloc: 100, HeapSys: 200, Sys: 300, HeapObjects: 4, NumGC: 5, LastGC: lastGC}

	got := ProjectMemory(stats, 1000, 250, true)
	wantLastGC := time.Unix(0, int64(lastGC))
	if !got.SystemAvailable || got.TotalBytes != 1000 || got.UsedBytes != 750 || got.AvailableBytes != 250 || got.UsagePercent != 75 ||
		got.HeapAllocBytes != 100 || got.HeapSysBytes != 200 || got.SysBytes != 300 || got.HeapObjects != 4 || got.GCCount != 5 || got.LastGCAt == nil || !got.LastGCAt.Equal(wantLastGC) {
		t.Fatalf("ProjectMemory() = %#v", got)
	}

	unavailable := ProjectMemory(runtime.MemStats{}, 900, 300, false)
	if unavailable.SystemAvailable || unavailable.UsedBytes != 0 || unavailable.UsagePercent != 0 || unavailable.TotalBytes != 900 || unavailable.AvailableBytes != 300 || unavailable.LastGCAt != nil {
		t.Fatalf("ProjectMemory(unavailable) = %#v", unavailable)
	}
	invalidTotals := ProjectMemory(runtime.MemStats{}, 100, 200, true)
	if invalidTotals.UsedBytes != 0 || invalidTotals.UsagePercent != 0 || invalidTotals.TotalBytes != 100 || invalidTotals.AvailableBytes != 200 {
		t.Fatalf("ProjectMemory(total < available) = %#v", invalidTotals)
	}
	zeroTotal := ProjectMemory(runtime.MemStats{}, 0, 0, true)
	if !zeroTotal.SystemAvailable || zeroTotal.UsedBytes != 0 || zeroTotal.UsagePercent != 0 {
		t.Fatalf("ProjectMemory(zero total) = %#v", zeroTotal)
	}
}

func memoryJSONKeys(t *testing.T, value MemorySnapshot) []string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestParseRedisInfo(t *testing.T) {
	info := "# Server\r\nredis_version:7.2.1\r\n\r\nuptime_in_seconds: 42 \r\nnote:no space:trimmed \r\ninvalid\r\nredis_version:7.2.2\r\n"
	got := ParseRedisInfo(info)
	want := map[string]string{
		"redis_version":     "7.2.2",
		"uptime_in_seconds": "42",
		"note":              "no space:trimmed",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseRedisInfo() = %#v, want %#v", got, want)
	}
	if empty := ParseRedisInfo(""); empty == nil || len(empty) != 0 {
		t.Fatalf("ParseRedisInfo(empty) = %#v, want non-nil empty map", empty)
	}
}

func TestRedisInfoInt(t *testing.T) {
	values := map[string]string{
		"valid":     "42",
		"negative":  "-7",
		"invalid":   "12x",
		"overflow":  "9223372036854775808",
		"underflow": "-9223372036854775809",
	}
	for _, test := range []struct {
		name string
		key  string
		want int64
	}{
		{name: "valid", key: "valid", want: 42},
		{name: "negative", key: "negative", want: -7},
		{name: "missing", key: "missing", want: 0},
		{name: "invalid", key: "invalid", want: 0},
		{name: "overflow", key: "overflow", want: 9223372036854775807},
		{name: "underflow", key: "underflow", want: -9223372036854775808},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := RedisInfoInt(values, test.key); got != test.want {
				t.Fatalf("RedisInfoInt(%q) = %d, want %d", test.key, got, test.want)
			}
		})
	}
	if got := RedisInfoInt(nil, "valid"); got != 0 {
		t.Fatalf("RedisInfoInt(nil) = %d, want 0", got)
	}
}

func TestRedisCacheHitRate(t *testing.T) {
	for _, test := range []struct {
		name   string
		hits   int64
		misses int64
		want   float64
	}{
		{name: "partial", hits: 3, misses: 1, want: 75},
		{name: "zero", hits: 0, misses: 0, want: 0},
		{name: "negative total", hits: -2, misses: 1, want: 0},
		{name: "negative hits", hits: -1, misses: 3, want: -50},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := RedisCacheHitRate(test.hits, test.misses); got != test.want {
				t.Fatalf("RedisCacheHitRate(%d, %d) = %v, want %v", test.hits, test.misses, got, test.want)
			}
		})
	}
}

func TestOverallStatus(t *testing.T) {
	healthy := HealthSignals{DatabaseConnected: true, DiskAvailable: true, DiskWritable: true, RuntimeValid: true}
	if got := OverallStatus(healthy); got != SystemHealthy {
		t.Fatalf("OverallStatus(healthy) = %q, want %q", got, SystemHealthy)
	}
	if got := OverallStatus(HealthSignals{DatabaseConnected: true, DiskAvailable: true, DiskWritable: true, RuntimeValid: true, RedisConfigured: true, RedisConnected: true}); got != SystemHealthy {
		t.Fatalf("OverallStatus(redis connected) = %q, want %q", got, SystemHealthy)
	}
	for _, test := range []struct {
		name    string
		signals HealthSignals
	}{
		{name: "database error", signals: HealthSignals{DatabaseError: true, DatabaseConnected: true, DiskAvailable: true, DiskWritable: true, RuntimeValid: true}},
		{name: "database disconnected", signals: HealthSignals{DiskAvailable: true, DiskWritable: true, RuntimeValid: true}},
		{name: "disk unavailable", signals: HealthSignals{DatabaseConnected: true, DiskWritable: true, RuntimeValid: true}},
		{name: "disk read-only", signals: HealthSignals{DatabaseConnected: true, DiskAvailable: true, RuntimeValid: true}},
		{name: "runtime invalid", signals: HealthSignals{DatabaseConnected: true, DiskAvailable: true, DiskWritable: true}},
		{name: "redis disconnected", signals: HealthSignals{DatabaseConnected: true, DiskAvailable: true, DiskWritable: true, RuntimeValid: true, RedisConfigured: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := OverallStatus(test.signals); got != SystemDegraded {
				t.Fatalf("OverallStatus() = %q, want %q", got, SystemDegraded)
			}
		})
	}
}
