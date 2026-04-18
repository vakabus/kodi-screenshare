package session

import "sync"

type State struct {
	mu     sync.Mutex
	active bool
}

func NewState() *State {
	return &State{}
}

func (s *State) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *State) SetActive(active bool) {
	s.mu.Lock()
	s.active = active
	s.mu.Unlock()
}
