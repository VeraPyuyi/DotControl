package model

// Postcondition is a bounded, read-only check. Text matches remain weak evidence.
// Task results require an independently supplied application_state observation.
type Postcondition struct {
	Kind          string `json:"kind"`
	Element       string `json:"element,omitempty"`
	Property      string `json:"property,omitempty"`
	Expected      string `json:"expected"`
	MaxWaitMillis int    `json:"max_wait_ms"`
}
