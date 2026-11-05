package app

import (
	"context"
	"strconv"
	"testing"

	cp "yingce/backend/internal/creationplatform"
)

// 一个只在测试里存在的新业务：海报生成器。它只用公开合同（Platform 接口与 BusinessRef），
// 不增加任何商品、镜头、节点之类的专属字段，就能完成提交、结果接收、费用汇总与按事件消费。
type posterBusiness struct {
	platform cp.Platform
	caller   cp.Caller
	channel  cp.ModelSelection
	cursor   string
	adopted  map[string]string // posterID -> resourceID，由业务自己记账
}

func (b *posterBusiness) requestPoster(posterID string) (string, error) {
	receipt, err := b.platform.Submit(context.Background(), b.caller, cp.GenerationRequest{
		ContractVersion: cp.ContractVersion, Capability: cp.CapabilityImage, Model: b.channel,
		Prompt: "节日海报 " + posterID, Parameters: map[string]any{"size": "1024x1024"},
		Business:       cp.BusinessRef{Application: "test", ProjectID: "poster-campaign", ObjectType: "poster", ObjectID: posterID, OperationID: posterID},
		IdempotencyKey: "poster:" + posterID,
	})
	return receipt.Snapshot.TaskID, err
}

// consume 按事件游标读取本业务的事件，终态成功时读取快照并采用结果；采用规则完全在业务内。
func (b *posterBusiness) consume(t *testing.T) {
	t.Helper()
	for {
		page, err := b.platform.Events(context.Background(), b.caller, cp.EventQuery{Application: "test", ProjectID: "poster-campaign", After: b.cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Events {
			if event.Execution != cp.ExecutionSucceeded || event.Resources != cp.ResourcesReady {
				continue
			}
			snapshot, err := b.platform.Task(context.Background(), b.caller, event.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Revision > event.Revision && snapshot.Execution != cp.ExecutionSucceeded {
				continue
			}
			b.adopted[snapshot.Business.ObjectID] = snapshot.Result.Resources[0].ResourceID
		}
		b.cursor = page.NextCursor
		if len(page.Events) == 0 {
			return
		}
	}
}

// S8-F：新增业务无需修改平台合同即可完成生成与结果接收。
func TestPlatformSupportsNewBusinessWithoutContractChanges(t *testing.T) {
	f := newPlatformImageFixture(t)
	business := &posterBusiness{platform: f.svc.CreationPlatform(), caller: cp.Caller{UserID: "user-a"}, channel: cp.ModelSelection{ChannelID: f.channelID, ModelKey: f.modelKey}, adopted: map[string]string{}}
	for index := 0; index < 2; index++ {
		if _, err := business.requestPoster("poster-" + strconv.Itoa(index)); err != nil {
			t.Fatal(err)
		}
	}
	// 业务侧重复请求同一张海报：平台回放原任务，不会生成两次。
	if _, err := business.requestPoster("poster-0"); err != nil {
		t.Fatal(err)
	}
	f.runQueued(t)
	f.runQueued(t)
	business.consume(t)
	if len(business.adopted) != 2 || business.adopted["poster-0"] == "" || business.adopted["poster-1"] == "" {
		t.Fatalf("new business did not receive results: %+v", business.adopted)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2", f.calls.Load())
	}
	aggregate, err := business.platform.BillingAggregate(context.Background(), business.caller, cp.BusinessRef{Application: "test", ProjectID: "poster-campaign"})
	if err != nil || aggregate.TaskCount != 2 || aggregate.ChargedMicrocredits != 200_000 {
		t.Fatalf("new business aggregate: %+v %v", aggregate, err)
	}
	// 再次消费不会重复采用（游标已前进）。
	before := business.cursor
	business.consume(t)
	if business.cursor != before {
		t.Fatalf("cursor moved without new events: %s -> %s", before, business.cursor)
	}
}
