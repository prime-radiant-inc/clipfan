package daemon

// seenCap bounds how many recent content hashes the daemon remembers.
const seenCap = 64

// seenSet is a bounded, insertion-ordered set of recently-seen content hashes.
// It tracks both content the daemon has already processed (relay/mesh dedup) and
// content it just wrote to its own clipboard (echo dedup). The oldest entry is
// evicted once the set exceeds seenCap.
//
// seenSet is not safe for concurrent use; callers must hold their own lock.
type seenSet struct {
	members map[[32]byte]struct{}
	order   [][32]byte
}

// newSeenSet returns an empty seenSet.
func newSeenSet() *seenSet {
	return &seenSet{members: make(map[[32]byte]struct{})}
}

// has reports whether h is currently in the set.
func (s *seenSet) has(h [32]byte) bool {
	_, ok := s.members[h]
	return ok
}

// add inserts h, evicting the oldest entry if the set is over capacity. Adding
// a hash already present is a no-op (it does not refresh its eviction order).
func (s *seenSet) add(h [32]byte) {
	if s.has(h) {
		return
	}
	s.members[h] = struct{}{}
	s.order = append(s.order, h)
	if len(s.order) > seenCap {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.members, oldest)
	}
}
