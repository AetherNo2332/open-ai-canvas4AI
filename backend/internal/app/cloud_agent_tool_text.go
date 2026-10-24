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
		cloudAgentToolTextCatalog = parseCloudAgentToolText(cloudAgentToolMarkdown)
	})
	value, ok := cloudAgentToolTextCatalog[key]
	if !ok {
		panic(fmt.Sprintf("missing Cloud Agent tool description: %s", key))
	}
	return value
}

func parseCloudAgentToolText(markdown string) map[string]string {
	// Checkout line endings must not change the schema sent to the model.
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	catalog := map[string]string{}
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
		if _, exists := catalog[heading]; exists {
			panic(fmt.Sprintf("duplicate Cloud Agent tool description: %s", heading))
		}
		catalog[heading] = value
	}
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			heading = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			body = nil
		} else if heading != "" {
			body = append(body, line)
		}
	}
	flush()
	return catalog
}
