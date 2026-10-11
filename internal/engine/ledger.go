package engine

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

type storedReceipt struct {
	model.Receipt
	Hash string `json:"request_hmac"`
}
type ledgerEvent struct {
	Version  int            `json:"version"`
	Type     string         `json:"type"`
	DeviceID string         `json:"device_id,omitempty"`
	Receipt  *storedReceipt `json:"receipt,omitempty"`
	TaskID   string         `json:"task_id,omitempty"`
	Paused   *bool          `json:"paused,omitempty"`
}
type envelope struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

func (e *Engine) sign(payload []byte) string {
	mac := hmac.New(sha256.New, e.key)
	mac.Write([]byte(e.chain))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
func (e *Engine) openLedger() error {
	keyPath := filepath.Join(e.cfg.StateDir, "ledger.key")
	walPath := filepath.Join(e.cfg.StateDir, "ledger.jsonl")
	hasKey, err := fileExists(keyPath)
	if err != nil {
		return fail("storage_unavailable")
	}
	hasWAL, err := fileExists(walPath)
	if err != nil {
		return fail("storage_unavailable")
	}
	if hasKey != hasWAL {
		return fail("ledger_corrupt")
	}
	if !hasKey {
		e.key = make([]byte, 32)
		if _, err = rand.Read(e.key); err != nil {
			return fail("storage_unavailable")
		}
		f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fail("storage_unavailable")
		}
		_, writeErr := f.Write(e.key)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return fail("storage_unavailable")
		}
	} else {
		e.key, err = os.ReadFile(keyPath)
		if err != nil {
			return fail("storage_unavailable")
		}
		if len(e.key) != 32 {
			return fail("ledger_corrupt")
		}
	}
	flags := os.O_RDWR | os.O_APPEND
	if !hasWAL {
		flags |= os.O_CREATE | os.O_EXCL
	}
	e.file, err = os.OpenFile(walPath, flags, 0600)
	if err != nil {
		return fail("storage_unavailable")
	}
	if !hasWAL {
		if err = e.appendEvent(ledgerEvent{Type: "init", DeviceID: e.cfg.DeviceID}); err != nil {
			return err
		}
		return syncDirectory(e.cfg.StateDir)
	}
	reader := bufio.NewReaderSize(e.file, 64*1024)
	first := true
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF && len(line) == 0 {
			break
		}
		if err != nil || len(line) > 64*1024 {
			return fail("ledger_corrupt")
		}
		var row envelope
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if dec.Decode(&row) != nil || len(row.Payload) == 0 || len(row.MAC) != 64 || e.sign(row.Payload) != row.MAC {
			return fail("ledger_corrupt")
		}
		if dec.Decode(new(any)) != io.EOF {
			return fail("ledger_corrupt")
		}
		var event ledgerEvent
		dec = json.NewDecoder(bytes.NewReader(row.Payload))
		dec.DisallowUnknownFields()
		if dec.Decode(&event) != nil || event.Version != 1 {
			return fail("ledger_corrupt")
		}
		if dec.Decode(new(any)) != io.EOF {
			return fail("ledger_corrupt")
		}
		if first {
			if event.Type != "init" || event.DeviceID != e.cfg.DeviceID {
				return fail("ledger_corrupt")
			}
			first = false
		} else if event.Type == "init" {
			return fail("ledger_corrupt")
		}
		if err = e.replay(event); err != nil {
			return err
		}
		e.chain = row.MAC
	}
	if first {
		return fail("ledger_corrupt")
	}
	// A durable dispatch intent with no durable outcome must never be dispatched
	// again. Recovery itself is durable before serving any entry point.
	for _, r := range e.receipts {
		if r.State == "dispatching" {
			r.State = "unknown"
			r.Code = "recovered_uncertain"
			r.UpdatedAt = time.Now().UTC()
			if err = e.saveReceiptLocked(r); err != nil {
				return err
			}
		}
	}
	return nil
}
func (e *Engine) replay(event ledgerEvent) error {
	switch event.Type {
	case "init":
		if event.Receipt != nil || event.TaskID != "" || event.Paused != nil {
			return fail("ledger_corrupt")
		}
	case "receipt":
		if event.Receipt == nil || event.TaskID != "" || event.Paused != nil || event.DeviceID != "" {
			return fail("ledger_corrupt")
		}
		r := event.Receipt.Receipt
		r.Hash = event.Receipt.Hash
		if r.DeviceID != e.cfg.DeviceID || !validID.MatchString(r.TaskID) || !validID.MatchString(r.ActionID) || len(r.Hash) != 64 || r.UpdatedAt.IsZero() || r.Message != "" {
			return fail("ledger_corrupt")
		}
		if _, err := hex.DecodeString(r.Hash); err != nil {
			return fail("ledger_corrupt")
		}
		switch r.State {
		case "dispatching", "succeeded", "unknown", "needs_user", "unsupported", "failed", "cancelled":
		default:
			return fail("ledger_corrupt")
		}
		if r.Mode != "" && r.Mode != "foreground" && r.Mode != "background" {
			return fail("ledger_corrupt")
		}
		if r.State == "needs_user" {
			r.Message = publicDriverHints[r.Code]
		}
		key := actionKey(r.TaskID, r.ActionID)
		if prev, ok := e.receipts[key]; ok {
			if prev.Hash != r.Hash || prev.State != "dispatching" || r.State == "dispatching" {
				return fail("ledger_corrupt")
			}
		}
		e.receipts[key] = r
	case "cancel":
		if !validID.MatchString(event.TaskID) || event.Receipt != nil || event.Paused != nil || event.DeviceID != "" {
			return fail("ledger_corrupt")
		}
		e.cancelled[event.TaskID] = true
	case "pause":
		if event.Paused == nil || event.Receipt != nil || event.TaskID != "" || event.DeviceID != "" {
			return fail("ledger_corrupt")
		}
		e.paused = *event.Paused
	default:
		return fail("ledger_corrupt")
	}
	return nil
}
func (e *Engine) appendEvent(event ledgerEvent) error {
	if e.broken {
		return fail("storage_unavailable")
	}
	event.Version = 1
	payload, err := json.Marshal(event)
	if err != nil {
		return e.storageFailure()
	}
	mac := e.sign(payload)
	line, err := json.Marshal(envelope{Payload: payload, MAC: mac})
	if err != nil {
		return e.storageFailure()
	}
	line = append(line, '\n')
	n, err := e.file.Write(line)
	if err != nil || n != len(line) {
		return e.storageFailure()
	}
	if e.syncFile(e.file) != nil {
		return e.storageFailure()
	}
	e.chain = mac
	return nil
}
func (e *Engine) storageFailure() error {
	e.broken = true
	if e.activeCancel != nil {
		e.activeCancel()
	}
	return fail("storage_unavailable")
}
