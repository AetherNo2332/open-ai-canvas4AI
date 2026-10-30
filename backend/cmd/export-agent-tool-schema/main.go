package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"yingce/backend/internal/service"
)

// Export the runtime catalog without starting the server or executing tests.
func main() {
	output := flag.String("output", "", "destination JSON file; stdout when omitted")
	flag.Parse()
	body, err := json.MarshalIndent(service.CloudAgentToolSchemaArtifact(), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	body = append(body, '\n')
	if *output == "" {
		_, err = os.Stdout.Write(body)
	} else {
		err = os.WriteFile(*output, body, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
