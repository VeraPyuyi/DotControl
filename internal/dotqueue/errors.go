package dotqueue

type Error struct {
	Code string
}

func (e *Error) Error() string {
	if e == nil || e.Code == "" {
		return "dot_queue_unknown"
	}
	return "dot_queue_" + e.Code
}

func errf(code string) error {
	return &Error{Code: code}
}

func asQueueError(err error) *Error {
	if err == nil {
		return nil
	}
	if qe, ok := err.(*Error); ok {
		return qe
	}
	return &Error{Code: "unknown"}
}

func wrapUnknown(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*Error); ok {
		return err
	}
	return errf("unknown")
}

func errorCode(err error) string {
	if qe := asQueueError(err); qe != nil {
		return qe.Code
	}
	return "unknown"
}
