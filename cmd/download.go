package main

import (
	"fmt"
	"midnight-go-sdk/compile"
	"midnight-go-sdk/logger"
	"os/exec"
	"strings"
)

// Download usage: go run cmd/main.go download
func Download() error {
	for _, ver := range compile.CompactVersions {
		cmd := exec.Command("compact", "update", ver)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start cmd fail %v", err)
		}
		logger.Logger().Info("Update compact version: " + ver)
		if err := cmd.Wait(); err != nil {
			if strings.Contains(err.Error(), "Unsupported compiler") {
				continue
			}
		}
	}
	return nil
}
