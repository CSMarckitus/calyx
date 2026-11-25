package instances

// PairingsOf gives the tests the pairing sessions of a service.
func PairingsOf(s *Service) *Pairings { return s.pairings }

// PairingEntries counts the entries of the map: the running sessions and the kept failures.
func PairingEntries(p *Pairings) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sessions)
}
