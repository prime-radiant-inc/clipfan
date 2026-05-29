package daemon

import "testing"

func hashOf(b byte) [32]byte {
	var h [32]byte
	h[0] = b
	return h
}

func TestSeenAddAndHas(t *testing.T) {
	s := newSeenSet()
	h := hashOf(1)
	if s.has(h) {
		t.Fatalf("empty set should not contain hash")
	}
	s.add(h)
	if !s.has(h) {
		t.Fatalf("set should contain added hash")
	}
}

func TestSeenDedup(t *testing.T) {
	s := newSeenSet()
	h := hashOf(1)
	s.add(h)
	s.add(h)
	if len(s.order) != 1 {
		t.Fatalf("duplicate add should not grow order; got %d", len(s.order))
	}
}

func TestSeenEviction(t *testing.T) {
	s := newSeenSet()
	// Fill exactly to capacity.
	for i := 0; i < seenCap; i++ {
		s.add(hashOf(byte(i)))
	}
	oldest := hashOf(0)
	if !s.has(oldest) {
		t.Fatalf("oldest entry should still be present at capacity")
	}
	// One more entry evicts the oldest.
	newest := hashOf(byte(seenCap))
	s.add(newest)
	if s.has(oldest) {
		t.Fatalf("oldest entry should have been evicted past capacity")
	}
	if !s.has(newest) {
		t.Fatalf("newest entry should be retained")
	}
	if len(s.order) != seenCap {
		t.Fatalf("order length should stay at cap; got %d", len(s.order))
	}
}
