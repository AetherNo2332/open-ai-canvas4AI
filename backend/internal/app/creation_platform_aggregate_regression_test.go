package app

import (
	"context"
	"testing"

	cp "yingce/backend/internal/creationplatform"
)

func TestPlatformAggregateTracksCurrentReservationAcrossRetry(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		ctx := context.Background()
		p := f.svc.CreationPlatform()
		caller := cp.Caller{UserID: "user-a"}
		req := f.systemRequest("aggregate-retry")
		receipt, err := p.Submit(ctx, caller, req)
		if err != nil {
			t.Fatal(err)
		}
		check := func(reserved, charged, refunded int64) {
			t.Helper()
			total, err := p.BillingAggregate(ctx, caller, cp.BusinessRef{Application: req.Business.Application})
			if err != nil {
				t.Fatal(err)
			}
			if total.TaskCount != 1 || total.ReservedMicrocredits != reserved || total.ChargedMicrocredits != charged || total.RefundedMicrocredits != refunded {
				t.Fatalf("totals=%+v, want reserved=%d charged=%d refunded=%d", total, reserved, charged, refunded)
			}
		}
		check(100_000, 0, 0)
		if _, err := p.Cancel(ctx, caller, receipt.Snapshot.TaskID); err != nil {
			t.Fatal(err)
		}
		check(0, 0, 100_000)
		if _, err := p.Retry(ctx, caller, receipt.Snapshot.TaskID); err != nil {
			t.Fatal(err)
		}
		check(100_000, 0, 100_000)
		f.runQueued(t)
		check(0, 100_000, 100_000)
	})
}
