package dotqueue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type canonicalSubmission struct {
	RequestID string             `json:"request_id"`
	Mode      string             `json:"mode"`
	SessionID string             `json:"session_id"`
	Messages  []canonicalMessage `json:"messages"`
	Task      string             `json:"task"`
}

type canonicalMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type canonicalResult struct {
	Text      string              `json:"text"`
	Artifacts []canonicalArtifact `json:"artifacts"`
	ErrorCode string              `json:"error_code"`
	Cancelled bool                `json:"cancelled"`
}

type canonicalArtifact struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

func submissionDigest(sub Submission) (string, error) {
	msgs := make([]canonicalMessage, len(sub.Messages))
	for i, m := range sub.Messages {
		msgs[i] = canonicalMessage{Role: m.Role, Content: m.Content}
	}
	body, err := json.Marshal(canonicalSubmission{
		RequestID: sub.RequestID,
		Mode:      sub.Mode,
		SessionID: sub.SessionID,
		Messages:  msgs,
		Task:      sub.Task,
	})
	if err != nil {
		return "", errf("unknown")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func resultDigest(res Result) (string, error) {
	arts := make([]canonicalArtifact, len(res.Artifacts))
	for i, a := range res.Artifacts {
		arts[i] = canonicalArtifact{URL: a.URL, Label: a.Label}
	}
	body, err := json.Marshal(canonicalResult{
		Text:      res.Text,
		Artifacts: arts,
		ErrorCode: res.ErrorCode,
		Cancelled: res.Cancelled,
	})
	if err != nil {
		return "", errf("unknown")
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
