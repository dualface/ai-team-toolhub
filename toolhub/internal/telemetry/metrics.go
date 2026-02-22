package telemetry

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var defaultRegistry = newRegistry()

type registry struct {
	mu                    sync.Mutex
	toolCalls             map[string]map[string]int64
	toolDurationBuckets   map[string][]int64
	artifactWriteFailures int64
	qaTimeouts            int64
	githubAPIErrors       map[string]map[int]int64
	repairIterations      map[string]int64
	repairQAResults       map[string]map[string]int64
	repairCompleted       map[string]int64
	repairRollbacks       map[string]int64
}

func newRegistry() *registry {
	return &registry{
		toolCalls:           make(map[string]map[string]int64),
		toolDurationBuckets: make(map[string][]int64),
		githubAPIErrors:     make(map[string]map[int]int64),
		repairIterations:    make(map[string]int64),
		repairQAResults:     make(map[string]map[string]int64),
		repairCompleted:     make(map[string]int64),
		repairRollbacks:     make(map[string]int64),
	}
}

func IncToolCall(toolName, status string) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	if _, ok := defaultRegistry.toolCalls[toolName]; !ok {
		defaultRegistry.toolCalls[toolName] = make(map[string]int64)
	}
	defaultRegistry.toolCalls[toolName][status]++
}

func ObserveToolDuration(toolName string, d time.Duration) {
	buckets := []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60}
	sec := d.Seconds()

	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	if _, ok := defaultRegistry.toolDurationBuckets[toolName]; !ok {
		defaultRegistry.toolDurationBuckets[toolName] = make([]int64, len(buckets)+1)
	}
	idx := len(buckets)
	for i, b := range buckets {
		if sec <= b {
			idx = i
			break
		}
	}
	defaultRegistry.toolDurationBuckets[toolName][idx]++
}

func IncArtifactWriteFailure() {
	defaultRegistry.mu.Lock()
	defaultRegistry.artifactWriteFailures++
	defaultRegistry.mu.Unlock()
}

func IncQATimeout() {
	defaultRegistry.mu.Lock()
	defaultRegistry.qaTimeouts++
	defaultRegistry.mu.Unlock()
}

func IncGitHubAPIError(operation string, statusCode int) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	if _, ok := defaultRegistry.githubAPIErrors[operation]; !ok {
		defaultRegistry.githubAPIErrors[operation] = make(map[int]int64)
	}
	defaultRegistry.githubAPIErrors[operation][statusCode]++
}

func IncRepairIteration(status string) {
	defaultRegistry.mu.Lock()
	defaultRegistry.repairIterations[status]++
	defaultRegistry.mu.Unlock()
}

func IncRepairQAResult(kind, status string) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	if _, ok := defaultRegistry.repairQAResults[kind]; !ok {
		defaultRegistry.repairQAResults[kind] = make(map[string]int64)
	}
	defaultRegistry.repairQAResults[kind][status]++
}

func IncRepairCompleted(outcome string) {
	defaultRegistry.mu.Lock()
	defaultRegistry.repairCompleted[outcome]++
	defaultRegistry.mu.Unlock()
}

func IncRepairRollback(status string) {
	defaultRegistry.mu.Lock()
	defaultRegistry.repairRollbacks[status]++
	defaultRegistry.mu.Unlock()
}

func RenderPrometheus() string {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()

	var sb strings.Builder

	sb.WriteString("# TYPE toolhub_tool_calls_total counter\n")
	toolNames := sortedKeys(defaultRegistry.toolCalls)
	for _, tool := range toolNames {
		statuses := sortedKeys(defaultRegistry.toolCalls[tool])
		for _, status := range statuses {
			sb.WriteString(fmt.Sprintf("toolhub_tool_calls_total{tool=\"%s\",status=\"%s\"} %d\n", tool, status, defaultRegistry.toolCalls[tool][status]))
		}
	}

	sb.WriteString("# TYPE toolhub_tool_duration_seconds_bucket counter\n")
	bucketLabels := []string{"0.1", "0.5", "1", "2", "5", "10", "30", "60", "+Inf"}
	for _, tool := range sortedKeys(defaultRegistry.toolDurationBuckets) {
		counts := defaultRegistry.toolDurationBuckets[tool]
		for i, v := range counts {
			sb.WriteString(fmt.Sprintf("toolhub_tool_duration_seconds_bucket{tool=\"%s\",le=\"%s\"} %d\n", tool, bucketLabels[i], v))
		}
	}

	sb.WriteString("# TYPE toolhub_artifact_write_failures_total counter\n")
	sb.WriteString(fmt.Sprintf("toolhub_artifact_write_failures_total %d\n", defaultRegistry.artifactWriteFailures))

	sb.WriteString("# TYPE toolhub_qa_timeouts_total counter\n")
	sb.WriteString(fmt.Sprintf("toolhub_qa_timeouts_total %d\n", defaultRegistry.qaTimeouts))

	sb.WriteString("# TYPE toolhub_github_api_errors_total counter\n")
	for _, op := range sortedKeys(defaultRegistry.githubAPIErrors) {
		statusCodes := make([]int, 0, len(defaultRegistry.githubAPIErrors[op]))
		for sc := range defaultRegistry.githubAPIErrors[op] {
			statusCodes = append(statusCodes, sc)
		}
		sort.Ints(statusCodes)
		for _, sc := range statusCodes {
			sb.WriteString(fmt.Sprintf("toolhub_github_api_errors_total{operation=\"%s\",status_code=\"%d\"} %d\n", op, sc, defaultRegistry.githubAPIErrors[op][sc]))
		}
	}

	sb.WriteString("# TYPE toolhub_repair_loop_iterations_total counter\n")
	for _, status := range sortedKeys(defaultRegistry.repairIterations) {
		sb.WriteString(fmt.Sprintf("toolhub_repair_loop_iterations_total{status=\"%s\"} %d\n", status, defaultRegistry.repairIterations[status]))
	}

	sb.WriteString("# TYPE toolhub_repair_loop_qa_results_total counter\n")
	for _, kind := range sortedKeys(defaultRegistry.repairQAResults) {
		statuses := sortedKeys(defaultRegistry.repairQAResults[kind])
		for _, status := range statuses {
			sb.WriteString(fmt.Sprintf("toolhub_repair_loop_qa_results_total{kind=\"%s\",status=\"%s\"} %d\n", kind, status, defaultRegistry.repairQAResults[kind][status]))
		}
	}

	sb.WriteString("# TYPE toolhub_repair_loop_completed_total counter\n")
	for _, outcome := range sortedKeys(defaultRegistry.repairCompleted) {
		sb.WriteString(fmt.Sprintf("toolhub_repair_loop_completed_total{outcome=\"%s\"} %d\n", outcome, defaultRegistry.repairCompleted[outcome]))
	}

	sb.WriteString("# TYPE toolhub_repair_loop_rollbacks_total counter\n")
	for _, status := range sortedKeys(defaultRegistry.repairRollbacks) {
		sb.WriteString(fmt.Sprintf("toolhub_repair_loop_rollbacks_total{status=\"%s\"} %d\n", status, defaultRegistry.repairRollbacks[status]))
	}

	return sb.String()
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
