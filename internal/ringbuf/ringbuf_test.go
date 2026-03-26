package ringbuf

import (
	"bytes"
	"sync"
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	rb := New(16)
	rb.Write([]byte("hello"))
	got := rb.Bytes()
	if !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("expected %q, got %q", "hello", got)
	}
	if rb.Len() != 5 {
		t.Fatalf("expected len 5, got %d", rb.Len())
	}
}

func TestOverflow(t *testing.T) {
	rb := New(5)
	// Write exactly cap bytes first
	rb.Write([]byte("ABCDE"))
	if rb.Len() != 5 {
		t.Fatalf("expected len 5, got %d", rb.Len())
	}
	// Write 3 more bytes; should overwrite oldest 3
	rb.Write([]byte("XYZ"))
	got := rb.Bytes()
	want := []byte("DEXYZ")
	if !bytes.Equal(got, want) {
		t.Fatalf("expected %q after overflow, got %q", want, got)
	}
}

func TestOverflowLargerThanCap(t *testing.T) {
	rb := New(4)
	// Write more than cap in a single call; only last cap bytes should be kept.
	rb.Write([]byte("ABCDEFGH"))
	got := rb.Bytes()
	want := []byte("EFGH")
	if !bytes.Equal(got, want) {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if rb.Len() != 4 {
		t.Fatalf("expected len 4, got %d", rb.Len())
	}
}

func TestMultipleWritesWithOverflow(t *testing.T) {
	rb := New(8)
	rb.Write([]byte("12345678")) // fills buffer
	rb.Write([]byte("ABCD"))     // overwrites first 4 bytes
	got := rb.Bytes()
	want := []byte("5678ABCD")
	if !bytes.Equal(got, want) {
		t.Fatalf("expected %q, got %q", want, got)
	}

	rb.Write([]byte("XYZ"))
	got = rb.Bytes()
	want = []byte("8ABCDXYZ")
	if !bytes.Equal(got, want) {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestConcurrentWritesAndReads(t *testing.T) {
	t.Parallel()
	rb := New(1024)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rb.Write([]byte("concurrentwrite"))
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = rb.Bytes()
				_ = rb.Len()
			}
		}()
	}
	wg.Wait()
	// After all writes, buffer should be non-empty and within capacity.
	l := rb.Len()
	if l < 0 || l > rb.Cap() {
		t.Fatalf("invalid len %d after concurrent ops", l)
	}
}

func TestReset(t *testing.T) {
	rb := New(8)
	rb.Write([]byte("hello"))
	rb.Reset()
	if rb.Len() != 0 {
		t.Fatalf("expected len 0 after reset, got %d", rb.Len())
	}
	got := rb.Bytes()
	if len(got) != 0 {
		t.Fatalf("expected empty bytes after reset, got %q", got)
	}
	// Can write again after reset.
	rb.Write([]byte("world"))
	got = rb.Bytes()
	if !bytes.Equal(got, []byte("world")) {
		t.Fatalf("expected %q after reset+write, got %q", "world", got)
	}
}

func TestEmptyBuffer(t *testing.T) {
	rb := New(8)
	if rb.Len() != 0 {
		t.Fatalf("expected empty buffer, got len %d", rb.Len())
	}
	if rb.Bytes() != nil {
		t.Fatalf("expected nil from empty Bytes()")
	}
	if rb.Cap() != 8 {
		t.Fatalf("expected cap 8, got %d", rb.Cap())
	}
}

func TestWriteEmpty(t *testing.T) {
	rb := New(8)
	rb.Write([]byte{})
	if rb.Len() != 0 {
		t.Fatalf("expected len 0 after writing empty slice")
	}
}

func TestWrapAround(t *testing.T) {
	rb := New(4)
	rb.Write([]byte("AB"))
	rb.Write([]byte("CD"))
	// Now full: ABCD
	rb.Write([]byte("EF"))
	// Overwrites AB: CDEF
	got := rb.Bytes()
	want := []byte("CDEF")
	if !bytes.Equal(got, want) {
		t.Fatalf("expected %q after wrap, got %q", want, got)
	}
}
