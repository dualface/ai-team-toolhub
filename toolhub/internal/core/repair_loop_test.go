package core

import (
	"errors"
	"testing"

	"github.com/toolhub/toolhub/internal/qa"
)

func TestMapQAStatusToMetric(t *testing.T) {
	tests := []struct {
		name  string
		input qa.Status
		want  string
	}{
		{name: "pass", input: qa.StatusPass, want: "pass"},
		{name: "fail", input: qa.StatusFail, want: "fail"},
		{name: "timeout", input: qa.StatusTimeout, want: "timeout"},
		{name: "error", input: qa.StatusError, want: "error"},
		{name: "dry_run", input: qa.StatusDryRun, want: "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapQAStatusToMetric(tt.input)
			if got != tt.want {
				t.Fatalf("MapQAStatusToMetric(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDeriveQAFailureCategory(t *testing.T) {
	testReportPass := qa.Report{ExitCode: 0}
	testReportFail := qa.Report{ExitCode: 2}
	lintReportPass := qa.Report{ExitCode: 0}

	tests := []struct {
		name    string
		testErr error
		lintErr error
		testRpt *qa.Report
		lintRpt *qa.Report
		want    string
	}{
		{
			name:    "both fail",
			testErr: errors.New("test failed"),
			lintErr: errors.New("lint failed"),
			want:    "both_failure",
		},
		{
			name:    "test timeout typed error",
			testErr: &qa.QAError{ErrCode: qa.ErrCodeTimeout, Detail: "timed out"},
			want:    "qa_timeout",
		},
		{
			name:    "test timeout string fallback",
			testErr: errors.New("operation timeout reached"),
			want:    "qa_timeout",
		},
		{
			name:    "test fail",
			testErr: errors.New("test failed"),
			want:    "test_failure",
		},
		{
			name:    "lint fail",
			lintErr: errors.New("lint failed"),
			want:    "lint_failure",
		},
		{
			name:    "reports indicate qa error",
			testRpt: &testReportFail,
			lintRpt: &lintReportPass,
			want:    "qa_error",
		},
		{
			name:    "no errors and passing reports",
			testRpt: &testReportPass,
			lintRpt: &lintReportPass,
			want:    "qa_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveQAFailureCategory(tt.testErr, tt.lintErr, tt.testRpt, tt.lintRpt)
			if got != tt.want {
				t.Fatalf("DeriveQAFailureCategory() = %q, want %q", got, tt.want)
			}
		})
	}
}
