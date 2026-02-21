package core

func DeriveBatchStatus(total, replayed, errCount int) string {
	if errCount <= 0 {
		return "ok"
	}
	if errCount == total-replayed {
		return "fail"
	}
	return "partial"
}
