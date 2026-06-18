package daemon

import "testing"

func TestNextClipboardPollIntervalBacksOffAfterIdlePolls(t *testing.T) {
	idlePolls := 0
	for i := 0; i < clipboardPollIdleAfter-1; i++ {
		interval := clipboardPollIdleInterval
		interval, idlePolls = nextClipboardPollInterval(idlePolls, false)
		if interval != clipboardPollActiveInterval {
			t.Fatalf("poll %d interval = %v, want active interval %v", i+1, interval, clipboardPollActiveInterval)
		}
	}

	interval, idlePolls := nextClipboardPollInterval(idlePolls, false)
	if interval != clipboardPollIdleInterval {
		t.Fatalf("idle interval = %v, want %v", interval, clipboardPollIdleInterval)
	}

	interval, idlePolls = nextClipboardPollInterval(idlePolls, true)
	if interval != clipboardPollActiveInterval || idlePolls != 0 {
		t.Fatalf("changed interval/count = %v/%d, want %v/0", interval, idlePolls, clipboardPollActiveInterval)
	}
}
