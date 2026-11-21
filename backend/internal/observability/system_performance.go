package observability

import (
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	SystemHealthy  = "healthy"
	SystemDegraded = "degraded"
)

type MemorySnapshot struct {
	SystemAvailable bool       `json:"systemAvailable"`
	TotalBytes      uint64     `json:"totalBytes"`
	UsedBytes       uint64     `json:"usedBytes"`
	AvailableBytes  uint64     `json:"availableBytes"`
	UsagePercent    float64    `json:"usagePercent"`
	HeapAllocBytes  uint64     `json:"heapAllocBytes"`
	HeapSysBytes    uint64     `json:"heapSysBytes"`
	SysBytes        uint64     `json:"sysBytes"`
	HeapObjects     uint64     `json:"heapObjects"`
	GCCount         uint32     `json:"gcCount"`
	LastGCAt        *time.Time `json:"lastGCAt,omitempty"`
}

func ProjectMemory(stats runtime.MemStats, total uint64, available uint64, systemAvailable bool) MemorySnapshot {
	used := uint64(0)
	percentage := float64(0)
	if systemAvailable && total >= available {
		used = total - available
		if total > 0 {
			percentage = float64(used) / float64(total) * 100
		}
	}
	result := MemorySnapshot{
		SystemAvailable: systemAvailable,
		TotalBytes:      total,
		UsedBytes:       used,
		AvailableBytes:  available,
		UsagePercent:    percentage,
		HeapAllocBytes:  stats.HeapAlloc,
		HeapSysBytes:    stats.HeapSys,
		SysBytes:        stats.Sys,
		HeapObjects:     stats.HeapObjects,
		GCCount:         stats.NumGC,
	}
	if stats.LastGC > 0 {
		last := time.Unix(0, int64(stats.LastGC))
		result.LastGCAt = &last
	}
	return result
}

func ParseRedisInfo(info string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found {
			values[key] = strings.TrimSpace(value)
		}
	}
	return values
}

func RedisInfoInt(values map[string]string, key string) int64 {
	value, _ := strconv.ParseInt(values[key], 10, 64)
	return value
}

func RedisCacheHitRate(hits int64, misses int64) float64 {
	if hits+misses <= 0 {
		return 0
	}
	return float64(hits) / float64(hits+misses) * 100
}

type HealthSignals struct {
	DatabaseError     bool
	DatabaseConnected bool
	DiskAvailable     bool
	DiskWritable      bool
	RuntimeValid      bool
	RedisConfigured   bool
	RedisConnected    bool
}

func OverallStatus(signals HealthSignals) string {
	if signals.DatabaseError ||
		!signals.DatabaseConnected ||
		!signals.DiskAvailable ||
		!signals.DiskWritable ||
		!signals.RuntimeValid ||
		(signals.RedisConfigured && !signals.RedisConnected) {
		return SystemDegraded
	}
	return SystemHealthy
}
