package server

import (
	"strings"
	"sync"
)

// Ring is a fixed-size ring buffer of console lines.
type Ring struct {
	mu    sync.RWMutex
	lines []string
	head  int // next write index
	size  int
	full  bool
}

// NewRing creates a ring buffer with the given capacity.
func NewRing(capacity int) *Ring {
	if capacity < 64 {
		capacity = 64
	}
	return &Ring{lines: make([]string, capacity)}
}

// Push appends a line to the buffer.
func (r *Ring) Push(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.head] = line
	r.head = (r.head + 1) % len(r.lines)
	if r.head == 0 {
		r.full = true
	}
	r.size++
}

// Last returns up to n most recent lines in chronological order.
// n negative or 0 means "all".
func (r *Ring) Last(n int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := len(r.lines)
	if !r.full {
		total = r.head
	}
	if total == 0 {
		return nil
	}
	if n <= 0 || n > total {
		n = total
	}
	out := make([]string, 0, n)
	start := (r.head - n + len(r.lines)) % len(r.lines)
	for i := 0; i < n; i++ {
		idx := (start + i) % len(r.lines)
		out = append(out, r.lines[idx])
	}
	return out
}

// Replay returns last n lines joined as-is (for tests / logs endpoint).
func (r *Ring) Replay(n int, strip bool) []string {
	lines := r.Last(n)
	if !strip {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimRight(l, "\n"))
	}
	return out
}
