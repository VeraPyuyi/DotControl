package dotevents

import "time"

type Error struct {
	Code       string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e == nil || e.Code == "" {
		return "dot_events_unknown"
	}
	return "dot_events_" + e.Code
}

func errf(code string) error { return &Error{Code: code} }

func retryErr(code string, after time.Duration) error {
	return &Error{Code: code, RetryAfter: after}
}

func asEventsError(err error) *Error {
	if err == nil {
		return nil
	}
	if ee, ok := err.(*Error); ok {
		return ee
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
