package core

import "fmt"

type PolicyViolationCode string

const (
	ViolationPathForbidden        PolicyViolationCode = "path_policy_forbidden"
	ViolationPathApprovalRequired PolicyViolationCode = "path_policy_approval_required"
	ViolationPathTraversal        PolicyViolationCode = "path_policy_traversal"
	ViolationPathEmpty            PolicyViolationCode = "path_policy_empty"
)

type PolicyViolation struct {
	Code   PolicyViolationCode `json:"code"`
	Path   string              `json:"path"`
	Reason string              `json:"reason"`
}

func (v *PolicyViolation) Error() string {
	return fmt.Sprintf("%s: %s (%s)", v.Code, v.Path, v.Reason)
}
