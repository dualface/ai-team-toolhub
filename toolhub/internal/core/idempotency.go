package core

type IdempotencyConflictError struct {
	Detail string
}

func (e *IdempotencyConflictError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return "idempotency key reused with different request payload"
}

func (e *IdempotencyConflictError) ErrorCode() string {
	return "idempotency_key_conflict"
}
