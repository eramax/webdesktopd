package ringbuf

import "sync"

// RingBuffer is a fixed-capacity circular byte buffer.
// Writes overwrite the oldest bytes when the buffer is full.
// All methods are safe for concurrent use.
type RingBuffer struct {
	buf  []byte
	head int // index of oldest byte (read position)
	tail int // index where next byte will be written
	size int // current number of bytes stored
	cap  int
	mu   sync.Mutex
}

// New creates a new RingBuffer with the given capacity.
func New(capacity int) *RingBuffer {
	if capacity <= 0 {
		panic("ringbuf: capacity must be positive")
	}
	return &RingBuffer{
		buf: make([]byte, capacity),
		cap: capacity,
	}
}

// Write appends p to the buffer, overwriting oldest bytes if necessary.
func (rb *RingBuffer) Write(p []byte) {
	if len(p) == 0 {
		return
	}
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(p) >= rb.cap {
		// Only keep the last cap bytes of p.
		p = p[len(p)-rb.cap:]
		copy(rb.buf, p)
		rb.head = 0
		rb.tail = 0
		rb.size = rb.cap
		return
	}

	for _, b := range p {
		rb.buf[rb.tail] = b
		rb.tail = (rb.tail + 1) % rb.cap
		if rb.size == rb.cap {
			// Overwrite oldest: advance head.
			rb.head = (rb.head + 1) % rb.cap
		} else {
			rb.size++
		}
	}
}

// Bytes returns a copy of all bytes currently in the buffer, in order (oldest first).
func (rb *RingBuffer) Bytes() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == 0 {
		return nil
	}
	out := make([]byte, rb.size)
	if rb.head+rb.size <= rb.cap {
		copy(out, rb.buf[rb.head:rb.head+rb.size])
	} else {
		n := copy(out, rb.buf[rb.head:])
		copy(out[n:], rb.buf[:rb.size-n])
	}
	return out
}

// Len returns the current number of bytes in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.size
}

// Cap returns the buffer capacity.
func (rb *RingBuffer) Cap() int {
	return rb.cap
}

// Reset clears the buffer.
func (rb *RingBuffer) Reset() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head = 0
	rb.tail = 0
	rb.size = 0
}
