package app

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
)

// openOrderStatuses 是"钱还锁着"的订单状态：reserved / running / uncertain。
// settled 与 refunded 是终态，不再占用预留额。
var openOrderStatuses = []string{
	string(model.BillingStatusReserved),
	string(model.BillingStatusRunning),
	string(model.BillingStatusUncertain),
}

// assertReservationInvariant 校验账户预留额与**未结订单预留额之和**对平。
//
// 这是计费正确性的核心不变量：任何"预留了但没释放"或"释放了两次"都会在这里暴露，
// 而这类缺陷在接口层面往往完全看不出来（运行照常成功，只是钱对不上）。
func assertReservationInvariant(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	var open []model.BillingOrder
	if err := db.Where("user_id = ? AND status IN ?", userID, openOrderStatuses).Find(&open).Error; err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, order := range open {
		sum += order.AmountMicrocredits
	}
	if account.ReservedMicrocredits != sum {
		t.Fatalf("预留额不对平：账户 reserved=%d，未结订单合计=%d（未结 %d 张）",
			account.ReservedMicrocredits, sum, len(open))
	}
}

// openOrdersFor 返回归属某个 run 的未结订单。
//
// 运行与订单的关联有两段：根任务的 ID 就是 runID（旧形态），后续步骤任务登记在
// state.TaskIDs 里。这里按"根任务 ID == runID"取，覆盖建 run 时那笔预授权。
func openOrdersFor(t *testing.T, db *gorm.DB, runID string) []model.BillingOrder {
	t.Helper()
	var orders []model.BillingOrder
	if err := db.Where("task_id = ? AND status IN ?", runID, openOrderStatuses).Find(&orders).Error; err != nil {
		t.Fatal(err)
	}
	return orders
}

// agentRunFixture 建一个可用的画布 + 账户余额，返回服务和请求。
func agentRunFixture(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	// 与既有 agent 用例一致：把余额抬到足够大，避免"余额不足"干扰计费断言。
	if err := db.Model(&model.CreditAccount{}).Where("user_id = ?", "user").
		Update("available_microcredits", int64(1_000_000_000)).Error; err != nil {
		t.Fatal(err)
	}
	assertReservationInvariant(t, db, "user") // 起点必须是对平的
	return s, db
}

// TestReservationInvariantHoldsAcrossRunLifecycle 是这次故障注入矩阵的**基线**。
//
// 建 run 会同时创建根任务与预授权订单（同一事务）；取消会走清理并释放。
// 两个节点都必须保持"账户预留额 == 未结订单之和"，否则就是钱对不上。
func TestReservationInvariantHoldsAcrossRunLifecycle(t *testing.T) {
	s, db := agentRunFixture(t)

	created, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatalf("建 run 失败: %v", err)
	}
	assertReservationInvariant(t, db, "user")

	orders := openOrdersFor(t, db, created.ID)
	if len(orders) != 1 {
		t.Fatalf("建 run 后应有且仅有 1 张未结预授权订单，实际 %d 张", len(orders))
	}
	if orders[0].AmountMicrocredits <= 0 {
		t.Fatalf("预授权金额必须为正: %d", orders[0].AmountMicrocredits)
	}

	if err := s.CancelCloudAgent(context.Background(), "user", created.ID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	assertReservationInvariant(t, db, "user")

	if left := openOrdersFor(t, db, created.ID); len(left) != 0 {
		t.Fatalf("终态运行不得留下未结订单，实际 %d 张（状态 %v）", len(left), left[0].Status)
	}
}

// TestRepeatedRunCreationDoesNotDoubleReserve：同一幂等键重复建 run 只能产生一笔预授权。
//
// 这是"重复投递"这一类故障的基本形态：客户端重试、响应丢失后重发、或两个入口同时提交。
func TestRepeatedRunCreationDoesNotDoubleReserve(t *testing.T) {
	s, db := agentRunFixture(t)

	first, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertReservationInvariant(t, db, "user")

	for attempt := 0; attempt < 3; attempt++ {
		repeat, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
		if err != nil {
			t.Fatalf("第 %d 次重投失败: %v", attempt+1, err)
		}
		if repeat.ID != first.ID {
			t.Fatalf("同一幂等键必须返回同一个运行：%s vs %s", repeat.ID, first.ID)
		}
		assertReservationInvariant(t, db, "user")
	}

	var total int64
	if err := db.Model(&model.BillingOrder{}).Where("task_id = ?", first.ID).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("同一幂等键只应有一张订单，实际 %d 张", total)
	}
}

// TestDifferentIdempotencyKeysReserveIndependently：不同幂等键是两次真实运行，
// 各自预授权，且不变量在两次之间仍然对平（防止把"独立预留"误判成重复预留）。
func TestDifferentIdempotencyKeysReserveIndependently(t *testing.T) {
	s, db := agentRunFixture(t)

	first, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	secondReq := agentTestRequest()
	secondReq.IdempotencyKey = "agent-test-key-2"
	second, err := s.CreateCloudAgentRun("user", secondReq, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("不同幂等键必须产生不同运行")
	}
	assertReservationInvariant(t, db, "user")

	if len(openOrdersFor(t, db, first.ID)) != 1 || len(openOrdersFor(t, db, second.ID)) != 1 {
		t.Fatal("两次运行应各有一笔独立预授权")
	}

	// 取消其一：另一笔必须原样保留，不变量仍对平。
	if err := s.CancelCloudAgent(context.Background(), "user", first.ID); err != nil {
		t.Fatal(err)
	}
	assertReservationInvariant(t, db, "user")
	if len(openOrdersFor(t, db, second.ID)) != 1 {
		t.Fatal("取消一个运行不得影响另一个运行的预授权")
	}
	if len(openOrdersFor(t, db, first.ID)) != 0 {
		t.Fatal("被取消的运行必须释放预授权")
	}
}

// TestConcurrentRunCreationReservesExactlyOnce 覆盖"重复投递"的并发形态：
// 同一个幂等键被两个入口**同时**提交。预授权必须恰好发生一次。
//
// 这是 Codex 验收清单里"重复请求不改变第二次账户余额"的并发版本 ——
// 顺序重试很容易写对，并发才是真正会漏的地方（任务与预授权同事务正是为此）。
func TestConcurrentRunCreationReservesExactlyOnce(t *testing.T) {
	s, db := agentRunFixture(t)

	const workers = 4
	type result struct {
		id  string
		err error
	}
	results := make(chan result, workers)
	start := make(chan struct{})
	for index := 0; index < workers; index++ {
		go func() {
			<-start
			run, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{id: run.ID}
		}()
	}
	close(start)

	ids := map[string]bool{}
	var firstErr error
	for index := 0; index < workers; index++ {
		got := <-results
		if got.err != nil {
			if firstErr == nil {
				firstErr = got.err
			}
			continue
		}
		ids[got.id] = true
	}
	if firstErr != nil {
		t.Fatalf("并发建 run 出现错误: %v", firstErr)
	}
	if len(ids) != 1 {
		t.Fatalf("同一幂等键的并发请求必须收敛到同一个运行，实际 %d 个: %v", len(ids), ids)
	}

	assertReservationInvariant(t, db, "user")
	var runID string
	for id := range ids {
		runID = id
	}
	var count int64
	if err := db.Model(&model.BillingOrder{}).Where("task_id = ?", runID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("并发建 run 只应产生 1 张订单，实际 %d 张", count)
	}
}

// TestConcurrentCancelReleasesReservationExactlyOnce 覆盖取消的幂等性：
// 重复取消（包括并发）不得把预留释放两次 —— 那会让账户预留额变成负数或与订单对不上。
func TestCancelCloudAgentAlreadyCleanedDoesNotReopenCleanup(t *testing.T) {
	s, db := agentRunFixture(t)
	created, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelCloudAgent(context.Background(), "user", created.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.repo.CloudAgent("user", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.CleanupPending {
		t.Fatal("first cancellation left pending cleanup")
	}
	if err := s.CancelCloudAgent(context.Background(), "user", created.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.repo.CloudAgent("user", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.CleanupPending {
		t.Fatalf("completed cancellation was reopened: before=%d after=%d pending=%v", before.Revision, after.Revision, after.CleanupPending)
	}
	assertReservationInvariant(t, db, "user")
}

func TestConcurrentCancelReleasesReservationExactlyOnce(t *testing.T) {
	s, db := agentRunFixture(t)

	created, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertReservationInvariant(t, db, "user")

	const workers = 4
	errs := make(chan error, workers)
	start := make(chan struct{})
	for index := 0; index < workers; index++ {
		go func() {
			<-start
			errs <- s.CancelCloudAgent(context.Background(), "user", created.ID)
		}()
	}
	close(start)
	var conflicts []error
	for index := 0; index < workers; index++ {
		if err := <-errs; err != nil {
			conflicts = append(conflicts, err)
		}
	}

	// 先断言钱：无论错误形态如何，预留都必须**恰好释放一次**。
	// 这一步先于错误断言，是为了把"金额对不对"与"错误码好不好看"分开评估。
	assertReservationInvariant(t, db, "user")
	if conflicts != nil {
		t.Fatalf("并发取消出现了 %d 个错误（首个: %v）；金额已对平，说明是错误面而非计费面问题",
			len(conflicts), conflicts[0])
	}
	if left := openOrdersFor(t, db, created.ID); len(left) != 0 {
		t.Fatalf("重复取消后不得残留未结订单，实际 %d 张", len(left))
	}
	view, err := s.CloudAgentRun("user", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "cancelled" {
		t.Fatalf("并发取消后状态 = %q，期望 cancelled", view.Status)
	}
}
