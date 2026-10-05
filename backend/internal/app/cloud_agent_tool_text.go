package app

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

//go:embed agent-tool-descriptions.md
var cloudAgentToolMarkdown string

var cloudAgentToolTextOnce sync.Once
var cloudAgentToolTextCatalog map[string]string

func cloudAgentToolText(key string) string {
	cloudAgentToolTextOnce.Do(func() {
		cloudAgentToolTextCatalog = map[string]string{}
		var heading string
		var body []string
		flush := func() {
			if heading == "" {
				return
			}
			value := strings.TrimSpace(strings.Join(body, "\n"))
			if value == "" {
				panic(fmt.Sprintf("empty Cloud Agent tool description: %s", heading))
			}
			if _, exists := cloudAgentToolTextCatalog[heading]; exists {
				panic(fmt.Sprintf("duplicate Cloud Agent tool description: %s", heading))
			}
			cloudAgentToolTextCatalog[heading] = value
		}
		for _, line := range strings.Split(cloudAgentToolMarkdown, "\n") {
			if strings.HasPrefix(line, "## ") {
				flush()
				heading = strings.TrimSpace(strings.TrimPrefix(line, "## "))
				body = nil
			} else if heading != "" {
				body = append(body, line)
			}
		}
		flush()
	})
	value, ok := cloudAgentToolTextCatalog[key]
	if !ok {
		panic(fmt.Sprintf("missing Cloud Agent tool description: %s", key))
	}
	return value
}
