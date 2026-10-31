package app

import (
	"context"
	"log"
	"time"
)

// startAgentWatchdog 是 Pi 运行的看门狗。Agent 循环由 agent/ 里的独立 Node worker 领取
// engine=pi 的运行；worker 崩溃或配置错误时，运行会被反复领取却永远不终结，
// 前端表现为"Agent 输出完了却一直运行中"。这里定期收敛这些运行。
func (s *Service) startAgentWatchdog() {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if !s.IsDraining() {
				if err := s.ReconcileDynamicSubagents(ctx); err != nil {
					log.Printf("subagent reconciliation: %v", err)
				}
				if _, err := s.SweepWorkerRecoveries(); err != nil {
					log.Printf("pi recovery sweep: %v", err)
				}
				if swept, err := s.SweepStalledPiAgentRuns(); err != nil {
					log.Printf("pi stalled sweep: %v", err)
				} else if swept > 0 {
					log.Printf("pi stalled sweep: terminated %d run(s)", swept)
				}
				// 从未被领取的运行是部署可用性问题（没起 agent / token 不一致 / 容器在重建），
				// 与 worker 崩溃语义不同，所以用更长阈值与独立原因分开清扫。
				if swept, err := s.SweepUnclaimedPiAgentRuns(); err != nil {
					log.Printf("pi unclaimed sweep: %v", err)
				} else if swept > 0 {
					log.Printf("pi unclaimed sweep: terminated %d run(s)", swept)
				}
				// 生成是异步的：运行即便已结束或取消，生成照常完成并写回画布。
				if settled, err := s.SettlePendingCloudAgentGenerations(); err != nil {
					log.Printf("pi generation settle: %v", err)
				} else if settled > 0 {
					log.Printf("pi generation settle: %d run(s)", settled)
				}
				// 清扫只写终态与 CleanupPending；退预留、取消子任务、释放租约在这里补做，
				// 否则看门狗停掉的运行会把占位预留冻在账上。
				if cleaned, err := s.DrainPendingPiAgentCleanups(); err != nil {
					log.Printf("pi cleanup drain: %v", err)
				} else if cleaned > 0 {
					log.Printf("pi cleanup drain: finished %d run(s)", cleaned)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}
