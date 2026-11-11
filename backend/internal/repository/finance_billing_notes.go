package repository

import (
	"fmt"
	"strings"
)

func tokenConsumptionNote(chargeCapped bool, supplement int64, priceRule int, usageSource string) string {
	consumeNote := ""
	if chargeCapped {
		consumeNote = "Token 实际用量超过 Agent 报价，已按本轮硬上限结算"
	} else if supplement > 0 {
		consumeNote = "Token 实际用量超过预授权，已补扣差额"
	}
	if priceRule > 0 {
		ruleNote := fmt.Sprintf("按分时与上下文价格第 %d 条结算", priceRule)
		if consumeNote != "" {
			ruleNote += "；" + consumeNote
		}
		consumeNote = ruleNote
	}
	if usageSource == billingUsageSourceVideoFormula {
		consumeNote = strings.TrimSpace("按提交时的视频 Token 公式快照结算；" + consumeNote)
	}
	return consumeNote
}
