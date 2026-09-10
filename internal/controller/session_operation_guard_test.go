package controller

import "testing"

func TestSessionOperationGuard(t *testing.T) {
	var g sessionOperationGuard
	release, ok := g.tryAcquire("one")
	if !ok {
		t.Fatal("first denied")
	}
	if _, ok = g.tryAcquire("one"); ok {
		t.Fatal("concurrent request admitted")
	}
	other, ok := g.tryAcquire("two")
	if !ok {
		t.Fatal("different session blocked")
	}
	other()
	release()
	release()
	if len(g.busy) != 0 {
		t.Fatal("leaked entries")
	}
	release, ok = g.tryAcquire("one")
	if !ok {
		t.Fatal("released session blocked")
	}
	release()
}
