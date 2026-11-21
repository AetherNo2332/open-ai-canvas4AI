package ecommerceskills

import "strings"

// ImageExecutionRules renders the selected skills' image constraints in the
// stable order returned by catalog selection.
func ImageExecutionRules(calls []ManagedInvocation) string {
	rules := make([]string, 0, len(calls))
	for _, call := range calls {
		if call.ImageRule != "" {
			rules = append(rules, call.Name+"："+call.ImageRule)
		}
	}
	if len(rules) == 0 {
		return ""
	}
	return "【本轮已选Skills图片执行约束；仅对适用页面执行】\n" + strings.Join(rules, "\n")
}
