package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/platform"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// platformDialects 让同一组 S2 验收分别跑在 SQLite 与隔离的 PostgreSQL schema 上；
// PostgreSQL 需要 CANVAS_TEST_POSTGRES_DSN，协调使用 CANVAS_TEST_REDIS_URL 指向的隔离 Redis。
func platformDialects(t *testing.T, run func(t *testing.T, f *platformImageFixture)) {
	t.Run("sqlite", func(t *testing.T) { run(t, newPlatformImageFixture(t)) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
		}
		run(t, newPlatformImageFixtureOn(t, platformPostgresDB(t, dsn)))
	})
}

func platformPostgresDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	options := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), options)
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, _ := admin.DB()
	t.Cleanup(func() { _ = adminSQL.Close() })
	schema := fmt.Sprintf("platform_s2_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error })
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), options)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func platformTestCoordinator(t *testing.T, dialect string) *platform.Coordinator {
	t.Helper()
	if dialect != "postgres" {
		return platform.NewCoordinatorWithRedis(nil, "platform-test")
	}
	address := os.Getenv("CANVAS_TEST_REDIS_URL")
	if address == "" {
		t.Skip("CANVAS_TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(address)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return platform.NewCoordinatorWithRedis(client, "platform-test")
}

func platformCount(t *testing.T, db *gorm.DB, value any, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(value).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func platformAvailable(t *testing.T, db *gorm.DB, userID string) (int64, int64) {
	t.Helper()
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	return account.AvailableMicrocredits, account.ReservedMicrocredits
}

// S2-A：20 个并发同键同内容请求只产生 1 个任务、1 份冻结、1 条提交记录，所有成功响应指向同一任务。
func TestPlatformConcurrentIdempotentSubmit(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		req := f.systemRequest("concurrent-key")
		const workers = 20
		ids := make([]string, workers)
		errs := make([]error, workers)
		replayed := make([]bool, workers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for index := 0; index < workers; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				receipt, err := p.Submit(context.Background(), cp.Caller{UserID: "user-a"}, req)
				ids[index], errs[index], replayed[index] = receipt.Snapshot.TaskID, err, receipt.Replayed
			}(index)
		}
		close(start)
		wg.Wait()
		fresh := 0
		for index := range ids {
			if errs[index] != nil {
				t.Fatalf("submit %d: %v", index, errs[index])
			}
			if ids[index] != ids[0] {
				t.Fatalf("responses point to different tasks: %v", ids)
			}
			if !replayed[index] {
				fresh++
			}
		}
		if fresh != 1 {
			t.Fatalf("fresh receipts = %d, want exactly 1", fresh)
		}
		if got := platformCount(t, f.db, &model.Task{}, "user_id = ?", "user-a"); got != 1 {
			t.Fatalf("tasks = %d", got)
		}
		if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != 1 {
			t.Fatalf("billing orders = %d", got)
		}
		if got := platformCount(t, f.db, &model.PlatformSubmission{}, "user_id = ?", "user-a"); got != 1 {
			t.Fatalf("submissions = %d", got)
		}
		available, reserved := platformAvailable(t, f.db, "user-a")
		if available != 10_000_000-100_000 || reserved != 100_000 {
			t.Fatalf("credits available=%d reserved=%d, want one reservation", available, reserved)
		}
		if f.calls.Load() != 0 {
			t.Fatal("submit must not call the provider")
		}
	})
}

// S2-B：同键改内容冲突；不同用户同键互不影响；响应丢失后重投取回原任务。
func TestPlatformIdempotencyScopeAndConflict(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		ctx := context.Background()
		req := f.systemRequest("scope-key")
		first, err := p.Submit(ctx, cp.Caller{UserID: "user-a"}, req)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := p.Submit(ctx, cp.Caller{UserID: "user-a"}, req)
		if err != nil || !replay.Replayed || replay.Snapshot.TaskID != first.Snapshot.TaskID {
			t.Fatalf("lost-response replay: %+v %v", replay, err)
		}
		traced := req
		traced.TraceID = "another-trace"
		if receipt, err := p.Submit(ctx, cp.Caller{UserID: "user-a"}, traced); err != nil || receipt.Snapshot.TaskID != first.Snapshot.TaskID {
			t.Fatalf("trace id must not break idempotency: %+v %v", receipt, err)
		}
		for name, mutate := range map[string]func(*cp.GenerationRequest){
			"prompt": func(r *cp.GenerationRequest) { r.Prompt = "另一个提示词" },
			"model":  func(r *cp.GenerationRequest) { r.Model = cp.ModelSelection{LogicalModelID: f.logicalID} },
			"reference": func(r *cp.GenerationRequest) {
				r.References = []cp.ReferenceResource{{ResourceID: "ref-a", Kind: "image"}}
			},
		} {
			changed := f.systemRequest("scope-key")
			mutate(&changed)
			if _, err := p.Submit(ctx, cp.Caller{UserID: "user-a"}, changed); platformErrorCode(err) != cp.CodeIdempotencyConflict {
				t.Fatalf("%s change with same key: %v", name, err)
			}
		}
		other, err := p.Submit(ctx, cp.Caller{UserID: "user-b"}, req)
		if err != nil || other.Replayed || other.Snapshot.TaskID == first.Snapshot.TaskID {
			t.Fatalf("user B with same key must get its own task: %+v %v", other, err)
		}
		otherApp := req
		otherApp.Business.Application = "drama"
		if receipt, err := p.Submit(ctx, cp.Caller{UserID: "user-a"}, otherApp); err != nil || receipt.Snapshot.TaskID == first.Snapshot.TaskID {
			t.Fatalf("same key in another application is a separate scope: %+v %v", receipt, err)
		}
		if got := platformCount(t, f.db, &model.Task{}, "1 = 1"); got != 3 {
			t.Fatalf("tasks = %d, want 3", got)
		}
	})
}

// S2-C：在任务、账单、提交记录写入处分别注入失败，整个事务回滚，不留孤儿也不提前执行。
func TestPlatformSubmitRollbackAtEachWrite(t *testing.T) {
	for _, table := range []string{"tasks", "billing_orders", "platform_submissions", "credit_ledger_entries"} {
		t.Run(table, func(t *testing.T) {
			platformDialects(t, func(t *testing.T, f *platformImageFixture) {
				dropTrigger := injectInsertFailure(t, f.db, table)
				_, err := f.svc.CreationPlatform().Submit(context.Background(), cp.Caller{UserID: "user-a"}, f.systemRequest("rollback-"+table))
				if err == nil {
					t.Fatal("submit should fail when a write fails")
				}
				for _, check := range []struct {
					value any
					name  string
				}{{&model.Task{}, "tasks"}, {&model.BillingOrder{}, "billing_orders"}, {&model.PlatformSubmission{}, "platform_submissions"}, {&model.CreditLedgerEntry{}, "credit_ledger_entries"}} {
					if got := platformCount(t, f.db, check.value, "user_id = ?", "user-a"); got != 0 {
						t.Fatalf("%s left %d rows after failure at %s", check.name, got, table)
					}
				}
				available, reserved := platformAvailable(t, f.db, "user-a")
				if available != 10_000_000 || reserved != 0 {
					t.Fatalf("credits changed after rollback: available=%d reserved=%d", available, reserved)
				}
				if claimed, err := f.svc.repo.ClaimNextTask("probe", time.Minute); err != nil || claimed != nil {
					t.Fatalf("rolled back submit left a claimable task: %+v %v", claimed, err)
				}
				dropTrigger()
				if _, err := f.svc.CreationPlatform().Submit(context.Background(), cp.Caller{UserID: "user-a"}, f.systemRequest("rollback-"+table)); err != nil {
					t.Fatalf("same key after rollback must be usable: %v", err)
				}
				if f.calls.Load() != 0 {
					t.Fatal("no provider call during submit")
				}
			})
		})
	}
}

// injectInsertFailure 让指定表的 INSERT 在数据库层失败，返回撤销函数。
func injectInsertFailure(t *testing.T, db *gorm.DB, table string) func() {
	t.Helper()
	name := "fail_insert_" + table
	if db.Dialector.Name() == "postgres" {
		statements := []string{
			"CREATE OR REPLACE FUNCTION " + name + "() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END; $$ LANGUAGE plpgsql",
			"CREATE TRIGGER " + name + " BEFORE INSERT ON " + table + " FOR EACH ROW EXECUTE FUNCTION " + name + "()",
		}
		for _, statement := range statements {
			if err := db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
		return func() { db.Exec("DROP TRIGGER " + name + " ON " + table) }
	}
	if err := db.Exec("CREATE TRIGGER " + name + " BEFORE INSERT ON " + table + " BEGIN SELECT RAISE(ABORT, 'injected failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	return func() { db.Exec("DROP TRIGGER " + name) }
}

// S2-D：余额不足、费用上限、报价过期与模型变更在写库前拒绝；报价与冻结价格一致，余额变化不被缓存掩盖。
func TestPlatformQuoteAuthorizationAndBillingStates(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		ctx := context.Background()
		caller := cp.Caller{UserID: "user-a"}
		req := f.systemRequest("quote-1")
		quote, err := p.Quote(ctx, caller, req)
		if err != nil {
			t.Fatal(err)
		}
		if quote.AmountMicrocredits != 100_000 || quote.BillingMode != "fixed_request" || quote.QuoteHash == "" || !quote.ExpiresAt.After(time.Now()) {
			t.Fatalf("quote: %+v", quote)
		}
		if got := platformCount(t, f.db, &model.BillingOrder{}, "1 = 1"); got != 0 {
			t.Fatal("quote must not reserve credits")
		}

		capped := req
		capped.IdempotencyKey = "quote-capped"
		capped.Cost.MaxMicrocredits = ptr[int64](99_999)
		if _, err := p.Submit(ctx, caller, capped); platformErrorCode(err) != cp.CodeCostLimitExceeded {
			t.Fatalf("cap below quote: %v", err)
		}

		authorized := req
		authorized.IdempotencyKey = "quote-authorized"
		authorized.Cost = cp.CostAuthorization{MaxMicrocredits: ptr[int64](100_000), QuoteHash: quote.QuoteHash}
		receipt, err := p.Submit(ctx, caller, authorized)
		if err != nil {
			t.Fatalf("authorized submit: %v", err)
		}
		if receipt.Snapshot.Billing.ReservedMicrocredits != quote.AmountMicrocredits || receipt.Snapshot.Billing.AuthorizedMaxMicrocredits == nil || *receipt.Snapshot.Billing.AuthorizedMaxMicrocredits != 100_000 {
			t.Fatalf("reservation does not match quote: %+v", receipt.Snapshot.Billing)
		}
		var order model.BillingOrder
		if err := f.db.First(&order, "task_id = ?", receipt.Snapshot.TaskID).Error; err != nil || !order.ChargeLimitSet || order.ChargeLimitMicrocredits != 100_000 {
			t.Fatalf("billing order must carry the authorized cap: %+v %v", order, err)
		}

		// 涨价后旧报价失效：返回 quote_expired，不能静默按新价冻结。
		if err := f.db.Model(&model.ChannelModelPriceTier{}).Where("id = ?", "platform-tier").Updates(map[string]any{"unit_price_microcredits": 150_000, "price_version": 2}).Error; err != nil {
			t.Fatal(err)
		}
		f.svc.invalidateRouteCatalog()
		stale := req
		stale.IdempotencyKey = "quote-stale"
		stale.Cost.QuoteHash = quote.QuoteHash
		if _, err := p.Submit(ctx, caller, stale); platformErrorCode(err) != cp.CodeQuoteExpired {
			t.Fatalf("stale quote after price change: %v", err)
		}
		fresh, err := p.Quote(ctx, caller, req)
		if err != nil || fresh.AmountMicrocredits != 150_000 || fresh.QuoteHash == quote.QuoteHash {
			t.Fatalf("fresh quote after price change: %+v %v", fresh, err)
		}

		// 余额：读取的是当前账户而不是缓存；不足时拒绝且不写任务。
		balance, err := p.Balance(ctx, caller)
		if err != nil || balance.ReservedMicrocredits != 100_000 || balance.AvailableMicrocredits != 10_000_000-100_000 {
			t.Fatalf("balance: %+v %v", balance, err)
		}
		if err := f.db.Model(&model.CreditAccount{}).Where("user_id = ?", "user-a").Update("available_microcredits", 1).Error; err != nil {
			t.Fatal(err)
		}
		if balance, err = p.Balance(ctx, caller); err != nil || balance.AvailableMicrocredits != 1 {
			t.Fatalf("balance must reflect the database: %+v %v", balance, err)
		}
		poor := req
		poor.IdempotencyKey = "quote-poor"
		before := platformCount(t, f.db, &model.Task{}, "1 = 1")
		if _, err := p.Submit(ctx, caller, poor); platformErrorCode(err) != cp.CodeInsufficientCredits {
			t.Fatalf("insufficient credits: %v", err)
		}
		if platformCount(t, f.db, &model.Task{}, "1 = 1") != before {
			t.Fatal("insufficient credits left a task")
		}
	})
}

// S2-E：成功结算、未提交失败退款、费用不明待核对；按业务归属汇总时冻结、实收、退款与待核对分列。
func TestPlatformBillingOutcomesAndAggregate(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		ctx := context.Background()
		caller := cp.Caller{UserID: "user-a"}
		submit := func(key string) string {
			t.Helper()
			req := f.systemRequest(key)
			req.Business.ProjectID = "batch-9"
			receipt, err := p.Submit(ctx, caller, req)
			if err != nil {
				t.Fatal(err)
			}
			return receipt.Snapshot.TaskID
		}
		settledID := submit("outcome-settled")
		f.runQueued(t)
		f.failStatus.Store(400)
		refundedID := submit("outcome-refunded")
		if err := f.runQueuedResult(t); err == nil {
			t.Fatal("upstream 400 must fail the task")
		}
		f.failStatus.Store(0)
		pendingID := submit("outcome-pending")
		if err := f.db.Model(&model.BillingOrder{}).Where("task_id = ?", pendingID).Update("status", model.BillingStatusUncertain).Error; err != nil {
			t.Fatal(err)
		}

		read := func(id string) cp.TaskSnapshot {
			t.Helper()
			snapshot, err := p.Task(ctx, caller, id)
			if err != nil {
				t.Fatal(err)
			}
			return snapshot
		}
		if s := read(settledID); s.Billing.Status != cp.BillingSettled || s.Billing.ChargedMicrocredits == nil || *s.Billing.ChargedMicrocredits != 100_000 {
			t.Fatalf("settled: %+v", s.Billing)
		}
		if s := read(refundedID); s.Execution != cp.ExecutionFailed || s.Billing.Status != cp.BillingRefunded || s.Billing.ChargedMicrocredits != nil || s.Billing.RefundedMicrocredits != 100_000 {
			t.Fatalf("refunded: %+v %+v", s.Execution, s.Billing)
		}
		if s := read(pendingID); s.Billing.Status != cp.BillingPendingReview || s.Billing.ChargedMicrocredits != nil {
			t.Fatalf("pending review must keep charged unknown: %+v", s.Billing)
		}

		aggregate, err := p.BillingAggregate(ctx, caller, cp.BusinessRef{Application: "ecommerce", ProjectID: "batch-9"})
		if err != nil {
			t.Fatal(err)
		}
		want := cp.BillingAggregate{Application: "ecommerce", ProjectID: "batch-9", TaskCount: 3, ReservedMicrocredits: 100_000, ChargedMicrocredits: 100_000, RefundedMicrocredits: 100_000, PendingReviewCount: 1}
		if aggregate != want {
			t.Fatalf("aggregate = %+v, want %+v", aggregate, want)
		}
		if other, err := p.BillingAggregate(ctx, cp.Caller{UserID: "user-b"}, cp.BusinessRef{Application: "ecommerce", ProjectID: "batch-9"}); err != nil || other.TaskCount != 0 {
			t.Fatalf("aggregate must be scoped to the caller: %+v %v", other, err)
		}
		if _, err := p.BillingAggregate(ctx, caller, cp.BusinessRef{}); platformErrorCode(err) != cp.CodeInvalidRequest {
			t.Fatalf("aggregate without application: %v", err)
		}
	})
}
