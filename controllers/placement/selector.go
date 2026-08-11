package placement

import (
	"context"
	"fmt"
	"sync"
)

// Candidate represents an eligible management cluster with its DNS capacity.
type Candidate struct {
	Name        string   // Maestro consumer name / MC name
	BaseDomains []string // available DNS zones/domains for this MC
}

// Selector selects an MC and base domain from a list of candidates.
// googPartnerSolution is the value of the goog-partner-solution GCP resource label sourced
// from the region's Secret Manager; it is empty when not available (e.g. static candidates).
type Selector interface {
	Select(ctx context.Context, candidates []Candidate) (mcName, baseDomain, googPartnerSolution string, err error)
}

// RoundRobinSelector selects MCs round-robin and picks the first available base domain.
type RoundRobinSelector struct {
	mu      sync.Mutex
	counter int
}

// NewRoundRobinSelector creates a new RoundRobinSelector.
func NewRoundRobinSelector() *RoundRobinSelector {
	return &RoundRobinSelector{}
}

// Select picks the next MC in round-robin order and returns its first base domain.
// googPartnerSolution is always empty — RoundRobinSelector has no Secret Manager access.
// Returns error if no candidates available or no base domains.
func (s *RoundRobinSelector) Select(_ context.Context, candidates []Candidate) (string, string, string, error) {
	if len(candidates) == 0 {
		return "", "", "", fmt.Errorf("placement: no candidates available")
	}

	s.mu.Lock()
	idx := s.counter % len(candidates)
	s.counter++
	s.mu.Unlock()

	c := candidates[idx]
	if len(c.BaseDomains) == 0 {
		return "", "", "", fmt.Errorf("placement: candidate %q has no base domains", c.Name)
	}

	return c.Name, c.BaseDomains[0], "", nil
}
