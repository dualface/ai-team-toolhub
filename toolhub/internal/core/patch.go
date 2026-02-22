package core

import (
	"fmt"
	"strings"
)

func GenerateUnifiedDiff(path, originalContent, modifiedContent string) string {
	cleanPath := strings.TrimSpace(path)
	cleanPath = strings.TrimPrefix(cleanPath, "./")
	if cleanPath == "" {
		cleanPath = "unknown.txt"
	}

	origLines := splitLines(originalContent)
	modLines := splitLines(modifiedContent)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", cleanPath, cleanPath))
	b.WriteString(fmt.Sprintf("--- a/%s\n", cleanPath))
	b.WriteString(fmt.Sprintf("+++ b/%s\n", cleanPath))
	b.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@\n", len(origLines), len(modLines)))

	for _, line := range origLines {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range modLines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

func CountContentLines(content string) int {
	return len(splitLines(content))
}

func splitLines(content string) []string {
	if content == "" {
		return []string{}
	}
	trimmed := strings.ReplaceAll(content, "\r\n", "\n")
	trimmed = strings.TrimSuffix(trimmed, "\n")
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "\n")
}
