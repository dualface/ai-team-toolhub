package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	MaxIssueTitleLen = 256
	MaxIssueBodyLen  = 65536
	MaxIssueLabels   = 20
	MaxLabelLen      = 50
	MaxBatchSize     = 50
)

type idempotencyPayload struct {
	RunID    string   `json:"run_id"`
	ToolName string   `json:"tool_name"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Labels   []string `json:"labels"`
	Index    *int     `json:"index,omitempty"`
}

func ValidateIssueInput(title, body string, labels []string) error {
	t := strings.TrimSpace(title)
	if t == "" {
		return fmt.Errorf("title is required")
	}
	if len(t) > MaxIssueTitleLen {
		return fmt.Errorf("title exceeds %d characters", MaxIssueTitleLen)
	}
	if len(body) > MaxIssueBodyLen {
		return fmt.Errorf("body exceeds %d characters", MaxIssueBodyLen)
	}
	if len(labels) > MaxIssueLabels {
		return fmt.Errorf("labels exceed %d items", MaxIssueLabels)
	}
	for _, label := range labels {
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("labels must not contain empty values")
		}
		if len(label) > MaxLabelLen {
			return fmt.Errorf("label exceeds %d characters", MaxLabelLen)
		}
	}
	return nil
}

func MakeIssueIdempotencyKey(runID, toolName, title, body string, labels []string, index *int) (string, error) {
	canonicalLabels := make([]string, 0, len(labels))
	for _, label := range labels {
		canonicalLabels = append(canonicalLabels, strings.TrimSpace(label))
	}
	sort.Strings(canonicalLabels)

	p := idempotencyPayload{
		RunID:    runID,
		ToolName: toolName,
		Title:    strings.TrimSpace(title),
		Body:     body,
		Labels:   canonicalLabels,
		Index:    index,
	}

	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal idempotency payload: %w", err)
	}

	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
