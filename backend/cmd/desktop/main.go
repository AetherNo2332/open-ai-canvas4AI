package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"infinite-canvas/backend/internal/desktop"
	"io"
	"os"
	"os/signal"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	input := bufio.NewReaderSize(os.Stdin, 64<<10)
	line, err := input.ReadSlice('\n')
	if err != nil {
		return errors.New("private bootstrap message unavailable")
	}
	var request struct {
		Protocol       int    `json:"protocol"`
		DataDir        string `json:"dataDir"`
		WebDir         string `json:"webDir"`
		MasterKey      string `json:"masterKey"`
		BootstrapToken string `json:"bootstrapToken"`
	}
	if json.Unmarshal(line, &request) != nil || request.Protocol != 1 {
		return errors.New("invalid private bootstrap protocol")
	}
	key, err := base64.StdEncoding.DecodeString(request.MasterKey)
	if err != nil || len(key) != 32 {
		return errors.New("invalid protected master key")
	}
	request.MasterKey = ""
	clear(line)
	defer clear(key)
	// Closing the parent pipe also shuts down the host after a renderer/main crash.
	go func() { _, _ = io.Copy(io.Discard, input); cancel() }()
	return desktop.Run(ctx, desktop.Config{DataDir: request.DataDir, WebDir: request.WebDir, MasterKey: key, BootstrapToken: request.BootstrapToken, Ready: os.Stdout})
}

func main() {
	if err := run(); err != nil {
		// Error causes are written only to the private process stream. Never
		// serialize bootstrap inputs or secret values into logs.
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"type": "error", "code": "host_start_failed"})
		_, _ = os.Stderr.WriteString("desktop host: " + err.Error() + "\n")
		os.Exit(1)
	}
}
