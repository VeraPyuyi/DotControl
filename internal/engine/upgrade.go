package engine

// UpgradeReady is a local lifecycle check, not proof that an old unknown action
// succeeded. Finalized receipts stay unchanged; unresolved live execution blocks.
func (e *Engine) UpgradeReady() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.healthyLocked(); err != nil {
		return err
	}
	if e.activeDone != nil {
		return fail("upgrade_busy")
	}
	if e.externalPending != "" {
		return fail("upgrade_unresolved")
	}
	// A cancelled call can return an unknown receipt before the driver and
	// release goroutines actually stop. They retain the sole-writer gate.
	select {
	case <-e.gate:
		e.unlock()
	default:
		return fail("upgrade_busy")
	}
	return nil
}
