package app

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"time"

	"yingce/backend/internal/buildinfo"
	"yingce/backend/internal/model"
	"yingce/backend/internal/observability"
	"yingce/backend/internal/repository"

	"github.com/redis/go-redis/v9"
)

var systemProcessStartedAt = time.Now()

const systemPerformanceTimeout = 3 * time.Second

type AdminSystemPerformance struct {
	CollectedAt time.Time                       `json:"collectedAt"`
	Status      string                          `json:"status"`
	Host        SystemPerformanceHost           `json:"host"`
	Memory      SystemPerformanceMemory         `json:"memory"`
	Disk        SystemPerformanceDisk           `json:"disk"`
	Database    repository.DatabaseRuntimeStats `json:"database"`
	Redis       SystemPerformanceRedis          `json:"redis"`
	Build       buildinfo.Info                  `json:"build"`
}

type SystemPerformanceHost struct {
	Hostname             string     `json:"hostname"`
	OS                   string     `json:"os"`
	Arch                 string     `json:"arch"`
	CPUCores             int        `json:"cpuCores"`
	GOMAXPROCS           int        `json:"gomaxprocs"`
	ProcessID            int        `json:"processId"`
	UptimeSeconds        int64      `json:"uptimeSeconds"`
	Goroutines           int        `json:"goroutines"`
	ActiveWorkerTasks    int64      `json:"activeWorkerTasks"`
	LoadAverage          [3]float64 `json:"loadAverage"`
	LoadAverageAvailable bool       `json:"loadAverageAvailable"`
}

type SystemPerformanceMemory struct {
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

type SystemPerformanceDisk struct {
	Available    bool    `json:"available"`
	Writable     bool    `json:"writable"`
	TotalBytes   uint64  `json:"totalBytes"`
	UsedBytes    uint64  `json:"usedBytes"`
	FreeBytes    uint64  `json:"freeBytes"`
	UsagePercent float64 `json:"usagePercent"`
}

type SystemPerformanceRedisPool struct {
	Hits             uint32 `json:"hits"`
	Misses           uint32 `json:"misses"`
	Timeouts         uint32 `json:"timeouts"`
	TotalConnections uint32 `json:"totalConnections"`
	IdleConnections  uint32 `json:"idleConnections"`
	StaleConnections uint32 `json:"staleConnections"`
	PendingRequests  uint32 `json:"pendingRequests"`
}

type SystemPerformanceCacheGroup struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Keys      int64  `json:"keys"`
	Clearable bool   `json:"clearable"`
}

type SystemPerformanceRedis struct {
	Configured      bool                          `json:"configured"`
	Connected       bool                          `json:"connected"`
	Mode            string                        `json:"mode"`
	LatencyMs       int64                         `json:"latencyMs,omitempty"`
	Version         string                        `json:"version,omitempty"`
	UptimeSeconds   int64                         `json:"uptimeSeconds,omitempty"`
	UsedMemoryBytes int64                         `json:"usedMemoryBytes,omitempty"`
	PeakMemoryBytes int64                         `json:"peakMemoryBytes,omitempty"`
	MaxMemoryBytes  int64                         `json:"maxMemoryBytes,omitempty"`
	MaxMemoryPolicy string                        `json:"maxMemoryPolicy,omitempty"`
	Keys            int64                         `json:"keys,omitempty"`
	Clients         int64                         `json:"clients,omitempty"`
	BlockedClients  int64                         `json:"blockedClients,omitempty"`
	OpsPerSecond    int64                         `json:"opsPerSecond,omitempty"`
	HitRate         float64                       `json:"hitRate,omitempty"`
	ExpiredKeys     int64                         `json:"expiredKeys,omitempty"`
	EvictedKeys     int64                         `json:"evictedKeys,omitempty"`
	Pool            SystemPerformanceRedisPool    `json:"pool"`
	CacheGroups     []SystemPerformanceCacheGroup `json:"cacheGroups"`
	StatusMessage   string                        `json:"statusMessage,omitempty"`
}

type AdminCacheClearRequest struct {
	Scope string `json:"scope"`
}

type AdminCacheClearGroupResult struct {
	ID          string `json:"id"`
	DeletedKeys int64  `json:"deletedKeys"`
}

type AdminCacheClearResult struct {
	Scope         string                       `json:"scope"`
	DeletedKeys   int64                        `json:"deletedKeys"`
	MemoryEntries int                          `json:"memoryEntries"`
	Groups        []AdminCacheClearGroupResult `json:"groups"`
	ClearedAt     time.Time                    `json:"clearedAt"`
}

type runtimeCacheDefinition struct {
	id, label, pattern string
}

var runtimeCacheDefinitions = []runtimeCacheDefinition{
	{id: "rateLimits", label: "请求频控", pattern: "canvas:rate:*"},
	{id: "circuitFailures", label: "渠道失败计数", pattern: "canvas:circuit:failures:*"},
	{id: "circuitOpen", label: "渠道熔断状态", pattern: "canvas:circuit:open:*"},
	{id: "routeHealth", label: "线路临时禁用", pattern: "canvas:logical-route-health:*"},
}

func (s *Service) AdminSystemPerformance(ctx context.Context, actor *model.User) (*AdminSystemPerformance, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, systemPerformanceTimeout)
	defer cancel()

	hostname, _ := os.Hostname()
	load, loadAvailable := platformLoadAverage()
	memory := collectSystemMemory()
	disk := collectSystemDisk(s.dataDir)
	databaseStats, databaseErr := s.repo.DatabaseRuntimeStats(ctx)
	redisStats := s.collectRedisPerformance(ctx)
	statusInputs := observability.HealthSignals{
		DatabaseError:     databaseErr != nil,
		DatabaseConnected: databaseStats.Connected,
		DiskAvailable:     disk.Available,
		DiskWritable:      disk.Writable,
		RedisConfigured:   redisStats.Configured,
		RedisConnected:    redisStats.Connected,
	}
	if !statusInputs.DatabaseError && statusInputs.DatabaseConnected && statusInputs.DiskAvailable && statusInputs.DiskWritable {
		statusInputs.RuntimeValid = s.ValidateRuntime() == nil
	}
	status := observability.OverallStatus(statusInputs)
	return &AdminSystemPerformance{
		CollectedAt: time.Now(), Status: status,
		Host:   SystemPerformanceHost{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUCores: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), ProcessID: os.Getpid(), UptimeSeconds: int64(time.Since(systemProcessStartedAt).Seconds()), Goroutines: runtime.NumGoroutine(), ActiveWorkerTasks: s.ActiveWorkerTasks(), LoadAverage: load, LoadAverageAvailable: loadAvailable},
		Memory: memory, Disk: disk, Database: databaseStats, Redis: redisStats, Build: buildinfo.Current(),
	}, nil
}

func collectSystemMemory() SystemPerformanceMemory {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	total, available, ok := platformMemory()
	snapshot := observability.ProjectMemory(stats, total, available, ok)
	return SystemPerformanceMemory{
		SystemAvailable: snapshot.SystemAvailable,
		TotalBytes:      snapshot.TotalBytes,
		UsedBytes:       snapshot.UsedBytes,
		AvailableBytes:  snapshot.AvailableBytes,
		UsagePercent:    snapshot.UsagePercent,
		HeapAllocBytes:  snapshot.HeapAllocBytes,
		HeapSysBytes:    snapshot.HeapSysBytes,
		SysBytes:        snapshot.SysBytes,
		HeapObjects:     snapshot.HeapObjects,
		GCCount:         snapshot.GCCount,
		LastGCAt:        snapshot.LastGCAt,
	}
}

func (s *Service) collectRedisPerformance(ctx context.Context) SystemPerformanceRedis {
	result := SystemPerformanceRedis{Mode: "local", StatusMessage: "未配置 Redis，当前使用单实例本地协调", CacheGroups: make([]SystemPerformanceCacheGroup, 0, len(runtimeCacheDefinitions))}
	if s.coordinator == nil {
		result.CacheGroups = append(result.CacheGroups, SystemPerformanceCacheGroup{ID: "rateLimits", Label: "本地请求频控", Clearable: true})
		return result
	}
	if !s.coordinator.HasRedis() {
		localRateKeys := int64(s.coordinator.LocalRateCount())
		result.CacheGroups = append(result.CacheGroups, SystemPerformanceCacheGroup{ID: "rateLimits", Label: "本地请求频控", Keys: localRateKeys, Clearable: true})
		return result
	}
	result.Configured, result.Mode = true, "redis"
	started := time.Now()
	if err := s.coordinator.Redis().Ping(ctx).Err(); err != nil {
		result.StatusMessage = "Redis 暂时不可用"
		return result
	}
	result.Connected = true
	result.LatencyMs = max(1, time.Since(started).Milliseconds())
	result.StatusMessage = "Redis 连接正常"
	if size, err := s.coordinator.Redis().DBSize(ctx).Result(); err == nil {
		result.Keys = size
	}
	if info, err := s.coordinator.Redis().Info(ctx, "server", "clients", "memory", "stats").Result(); err == nil {
		values := observability.ParseRedisInfo(info)
		result.Version = values["redis_version"]
		result.UptimeSeconds = observability.RedisInfoInt(values, "uptime_in_seconds")
		result.UsedMemoryBytes = observability.RedisInfoInt(values, "used_memory")
		result.PeakMemoryBytes = observability.RedisInfoInt(values, "used_memory_peak")
		result.MaxMemoryBytes = observability.RedisInfoInt(values, "maxmemory")
		result.MaxMemoryPolicy = values["maxmemory_policy"]
		result.Clients = observability.RedisInfoInt(values, "connected_clients")
		result.BlockedClients = observability.RedisInfoInt(values, "blocked_clients")
		result.OpsPerSecond = observability.RedisInfoInt(values, "instantaneous_ops_per_sec")
		result.ExpiredKeys = observability.RedisInfoInt(values, "expired_keys")
		result.EvictedKeys = observability.RedisInfoInt(values, "evicted_keys")
		hits, misses := observability.RedisInfoInt(values, "keyspace_hits"), observability.RedisInfoInt(values, "keyspace_misses")
		result.HitRate = observability.RedisCacheHitRate(hits, misses)
	}
	pool := s.coordinator.Redis().PoolStats()
	result.Pool = SystemPerformanceRedisPool{Hits: pool.Hits, Misses: pool.Misses, Timeouts: pool.Timeouts, TotalConnections: pool.TotalConns, IdleConnections: pool.IdleConns, StaleConnections: pool.StaleConns, PendingRequests: pool.PendingRequests}
	for _, definition := range runtimeCacheDefinitions {
		count, _ := scanRedisKeyCount(ctx, s.coordinator.Redis(), definition.pattern)
		result.CacheGroups = append(result.CacheGroups, SystemPerformanceCacheGroup{ID: definition.id, Label: definition.label, Keys: count, Clearable: true})
	}
	return result
}

func (s *Service) ClearAdminRuntimeCache(ctx context.Context, actor *model.User, request AdminCacheClearRequest) (*AdminCacheClearResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Scope) != "runtime" {
		return nil, BadAuthRequest("仅支持清理运行时缓存")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := &AdminCacheClearResult{Scope: "runtime", ClearedAt: time.Now(), Groups: make([]AdminCacheClearGroupResult, 0, len(runtimeCacheDefinitions))}
	if s.coordinator != nil {
		if s.coordinator.HasRedis() {
			for _, definition := range runtimeCacheDefinitions {
				deleted, err := deleteRedisKeysByPattern(ctx, s.coordinator.Redis(), definition.pattern)
				if err != nil {
					return nil, errors.New("清理 Redis 运行时缓存失败")
				}
				result.Groups = append(result.Groups, AdminCacheClearGroupResult{ID: definition.id, DeletedKeys: deleted})
				result.DeletedKeys += deleted
			}
		} else {
			count := s.coordinator.ClearLocalRate()
			result.DeletedKeys += int64(count)
			result.Groups = append(result.Groups, AdminCacheClearGroupResult{ID: "rateLimits", DeletedKeys: int64(count)})
		}
	}
	s.initReadCaches()
	result.MemoryEntries += s.concurrencyReadCache.ClearAndCount()
	result.MemoryEntries += s.textReplayReadCache.ClearAndCount()
	result.MemoryEntries += s.routeVersionReadCache.ClearAndCount()
	s.routeHealthMu.Lock()
	result.MemoryEntries += len(s.routeHealthBlocked)
	s.routeHealthBlocked = make(map[string]time.Time)
	s.routeHealthMu.Unlock()
	// 与路由目录刷新保持同一加锁顺序，避免清理动作和回源刷新并发写入重试状态。
	s.routeCatalogRefreshMu.Lock()
	s.routeCatalogMu.Lock()
	if s.routeCatalog != nil {
		result.MemoryEntries++
	}
	s.routeCatalog = nil
	s.routeCatalogVersion = 0
	s.routeCatalogRetryAt = time.Time{}
	s.routeCatalogRefreshError = nil
	s.routeCatalogMu.Unlock()
	s.routeCatalogRefreshMu.Unlock()
	if err := s.appendAdminAudit(actor, "system.runtime_cache.clear", "system", "runtime-cache", "清理安全运行时缓存", map[string]any{"deletedKeys": result.DeletedKeys, "memoryEntries": result.MemoryEntries}); err != nil {
		return nil, err
	}
	return result, nil
}

func scanRedisKeyCount(ctx context.Context, client *redis.Client, pattern string) (int64, error) {
	var cursor uint64
	var total int64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return total, err
		}
		total += int64(len(keys))
		cursor = next
		if cursor == 0 {
			return total, nil
		}
	}
}

func deleteRedisKeysByPattern(ctx context.Context, client *redis.Client, pattern string) (int64, error) {
	var cursor uint64
	var deleted int64
	for {
		keys, next, err := client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return deleted, err
		}
		if len(keys) > 0 {
			count, err := client.Unlink(ctx, keys...).Result()
			if err != nil {
				return deleted, err
			}
			deleted += count
		}
		cursor = next
		if cursor == 0 {
			return deleted, nil
		}
	}
}
