package codexbridge

// UpgradeReady checks async owned turns after the shared transport admission
// gate has drained. It never interrupts a turn or edits an old outcome.
func (b *Bridge) UpgradeReady() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.healthy(); err != nil {
		return err
	}
	if b.runtime == nil {
		return nil
	}
	if b.runtime.closing {
		return failure("closed")
	}
	for _, j := range b.runtime.jobs {
		if j.active || j.client != nil || j.lease != nil {
			return failure("upgrade_busy")
		}
		if j.entry.DesktopPending || !runtimeTerminal(j.entry.Job.State) {
			return failure("upgrade_unresolved")
		}
	}
	return nil
}
