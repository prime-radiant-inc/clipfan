package daemon

// seenCap bounds how many recent clip-IDs the daemon remembers for mesh dedup.
const seenCap = 256

// seenSet is a bounded, insertion-ordered set of recently-seen clip-IDs. The
// oldest entry is evicted once the set exceeds seenCap. Not safe for concurrent
// use; callers must hold their own lock.
type seenSet struct {
	members map[string]struct{}
	order   []string
}

func newSeenSet() *seenSet {
	return &seenSet{members: make(map[string]struct{})}
}

func (s *seenSet) has(id string) bool {
	_, ok := s.members[id]
	return ok
}

func (s *seenSet) add(id string) {
	if s.has(id) {
		return
	}
	s.members[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > seenCap {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.members, oldest)
	}
}
