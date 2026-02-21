package core

import (
	"fmt"
	"strings"
)

type BatchMode string

const (
	BatchModePartial BatchMode = "partial"
	BatchModeStrict  BatchMode = "strict"
)

func ParseBatchMode(v string) (BatchMode, error) {
	mode := BatchMode(strings.ToLower(strings.TrimSpace(v)))
	if mode == "" {
		return BatchModePartial, nil
	}
	if mode == BatchModePartial || mode == BatchModeStrict {
		return mode, nil
	}
	return "", fmt.Errorf("invalid BATCH_MODE %q, expected partial or strict", v)
}
