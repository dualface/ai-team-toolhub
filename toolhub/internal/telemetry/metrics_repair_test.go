package telemetry

import (
	"strings"
	"testing"
)

func TestRenderPrometheus_RepairMetricLabelOrderingStable(t *testing.T) {
	defaultRegistry = newRegistry()

	IncRepairIteration("pass")
	IncRepairIteration("fail")
	IncRepairQAResult("lint", "error")
	IncRepairQAResult("test", "timeout")
	IncRepairCompleted("success")
	IncRepairCompleted("qa_failed")
	IncRepairRollback("success")
	IncRepairRollback("failure")

	out := RenderPrometheus()

	iterFail := strings.Index(out, `toolhub_repair_loop_iterations_total{status="fail"}`)
	iterPass := strings.Index(out, `toolhub_repair_loop_iterations_total{status="pass"}`)
	if iterFail < 0 || iterPass < 0 {
		t.Fatal("repair iteration metrics missing from output")
	}

	qaLint := strings.Index(out, `toolhub_repair_loop_qa_results_total{kind="lint",status="error"}`)
	qaTest := strings.Index(out, `toolhub_repair_loop_qa_results_total{kind="test",status="timeout"}`)
	if qaLint < 0 || qaTest < 0 {
		t.Fatal("repair qa result metrics missing from output")
	}

	compFailed := strings.Index(out, `toolhub_repair_loop_completed_total{outcome="qa_failed"}`)
	compSuccess := strings.Index(out, `toolhub_repair_loop_completed_total{outcome="success"}`)
	if compFailed < 0 || compSuccess < 0 {
		t.Fatal("repair completion metrics missing from output")
	}

	rollFail := strings.Index(out, `toolhub_repair_loop_rollbacks_total{status="failure"}`)
	rollSuccess := strings.Index(out, `toolhub_repair_loop_rollbacks_total{status="success"}`)
	if rollFail < 0 || rollSuccess < 0 {
		t.Fatal("repair rollback metrics missing from output")
	}

	if iterFail >= iterPass {
		t.Fatal("repair iteration labels are not rendered in stable lexical order")
	}

	if qaLint >= qaTest {
		t.Fatal("repair qa result labels are not rendered in stable lexical order")
	}

	if compFailed >= compSuccess {
		t.Fatal("repair completed labels are not rendered in stable lexical order")
	}

	if rollFail >= rollSuccess {
		t.Fatal("repair rollback labels are not rendered in stable lexical order")
	}
}
