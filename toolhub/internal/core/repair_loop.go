package core

import (
	"errors"
	"strings"

	"github.com/toolhub/toolhub/internal/qa"
)

// MapQAStatusToMetric maps QA status values to stable metric labels.
func MapQAStatusToMetric(status qa.Status) string {
	switch status {
	case qa.StatusPass:
		return "pass"
	case qa.StatusFail:
		return "fail"
	case qa.StatusTimeout:
		return "timeout"
	default:
		return "error"
	}
}

// DeriveQAFailureCategory returns a stable failure category for repair-loop QA failures.
func DeriveQAFailureCategory(testErr, lintErr error, testReport, lintReport *qa.Report) string {
	if testErr != nil && lintErr != nil {
		return "both_failure"
	}
	if testErr != nil {
		var qaErr *qa.QAError
		if (errors.As(testErr, &qaErr) && qaErr.ErrCode == qa.ErrCodeTimeout) || strings.Contains(strings.ToLower(testErr.Error()), "timeout") {
			return "qa_timeout"
		}
		return "test_failure"
	}
	if lintErr != nil {
		return "lint_failure"
	}

	if testReport != nil && lintReport != nil {
		testStatus := qa.DeriveStatus(*testReport, nil, false)
		lintStatus := qa.DeriveStatus(*lintReport, nil, false)
		if testStatus != qa.StatusPass || lintStatus != qa.StatusPass {
			return "qa_error"
		}
	}

	return "qa_error"
}
