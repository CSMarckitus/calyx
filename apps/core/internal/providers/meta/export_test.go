package meta

// Hooks for the external tests of package meta_test.

// RetryArmed tells whether a background Connect is scheduled.
func RetryArmed(p *Provider) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.retry != nil
}
