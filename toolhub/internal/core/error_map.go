package core

import (
	"errors"
	"strings"
)

// CodedError is implemented by domain errors that carry a machine-readable code.
type CodedError interface {
	error
	ErrorCode() string
}

type ErrorInfo struct {
	Code       string
	Message    string
	HTTPStatus int
}

func MapError(err error, fallbackStatus int) ErrorInfo {
	if err == nil {
		return ErrorInfo{Code: "internal_error", Message: "internal server error", HTTPStatus: fallbackStatus}
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	var coded CodedError
	if errors.As(err, &coded) {
		code := coded.ErrorCode()
		switch code {
		case "qa_command_empty", "qa_command_invalid", "qa_workdir_invalid", "qa_tool_unsupported":
			return ErrorInfo{Code: code, Message: msg, HTTPStatus: 400}
		case "qa_command_not_allowed":
			return ErrorInfo{Code: code, Message: msg, HTTPStatus: 403}
		case "qa_timeout":
			return ErrorInfo{Code: code, Message: msg, HTTPStatus: 200}
		case "qa_execution_failed":
			return ErrorInfo{Code: code, Message: msg, HTTPStatus: 200}
		}
	}

	switch {
	case strings.Contains(lower, "repo") && strings.Contains(lower, "allowlist"):
		return ErrorInfo{Code: "repo_not_allowed", Message: msg, HTTPStatus: 403}
	case strings.Contains(lower, "tool") && strings.Contains(lower, "allowlist"):
		return ErrorInfo{Code: "tool_not_allowed", Message: msg, HTTPStatus: 403}
	case strings.Contains(lower, "run not found"):
		return ErrorInfo{Code: "run_not_found", Message: "run not found", HTTPStatus: 404}
	case strings.Contains(lower, "invalid json"), strings.Contains(lower, "request body must contain a single json object"):
		return ErrorInfo{Code: "invalid_request_schema", Message: msg, HTTPStatus: 400}
	case strings.Contains(lower, "title is required"), strings.Contains(lower, "exceeds"), strings.Contains(lower, "labels must not contain"):
		return ErrorInfo{Code: "invalid_request_schema", Message: msg, HTTPStatus: 400}
	case strings.Contains(lower, "discover installation id"), strings.Contains(lower, "no installation found"), strings.Contains(lower, "multiple installations found"):
		return ErrorInfo{Code: "app_not_installed", Message: msg, HTTPStatus: 502}
	case strings.Contains(lower, "http 403"):
		return ErrorInfo{Code: "github_permission_denied", Message: msg, HTTPStatus: 502}
	case strings.Contains(lower, "http 401"):
		return ErrorInfo{Code: "github_auth_failed", Message: msg, HTTPStatus: 502}
	case strings.Contains(lower, "http 404"):
		return ErrorInfo{Code: "github_not_found", Message: msg, HTTPStatus: 502}
	case strings.Contains(lower, "http 422"):
		return ErrorInfo{Code: "github_validation_failed", Message: msg, HTTPStatus: 400}
	default:
		code := "internal_error"
		if fallbackStatus >= 400 && fallbackStatus < 500 {
			code = "bad_request"
		}
		return ErrorInfo{Code: code, Message: msg, HTTPStatus: fallbackStatus}
	}
}
