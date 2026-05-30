package daemon

import "testing"

func idOf(i int) string {
	return "id-" + itoa(i)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestSeenSetAddAndHas(t *testing.T) {
	s := newSeenSet()
	id := idOf(1)
	if s.has(id) {
		t.Fatalf("empty set should not contain id")
	}
	s.add(id)
	if !s.has(id) {
		t.Fatalf("set should contain added id")
	}
}

func TestSeenSetDedup(t *testing.T) {
	s := newSeenSet()
	id := idOf(1)
	s.add(id)
	s.add(id)
	if len(s.order) != 1 {
		t.Fatalf("duplicate add should not grow order; got %d", len(s.order))
	}
}

func TestSeenSetEviction(t *testing.T) {
	s := newSeenSet()
	first := idOf(0)
	s.add(first)
	// Add seenCap more distinct ids; the first must be evicted once we exceed
	// capacity.
	for i := 1; i <= seenCap; i++ {
		s.add(idOf(i))
	}
	if s.has(first) {
		t.Fatalf("oldest entry should have been evicted past capacity")
	}
	newest := idOf(seenCap)
	if !s.has(newest) {
		t.Fatalf("newest entry should be retained")
	}
	if len(s.order) != seenCap {
		t.Fatalf("order length should stay at cap; got %d", len(s.order))
	}
}
