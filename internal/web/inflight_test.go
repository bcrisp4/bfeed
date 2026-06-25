package web

import (
	"sync"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestInflightStartDoneHas(t *testing.T) {
	s := newInflightSet()
	if s.has(1) {
		t.Fatal("empty set should not have 1")
	}
	if !s.start(1) {
		t.Fatal("first start should return true")
	}
	if s.start(1) {
		t.Fatal("second start of same id should return false")
	}
	if !s.has(1) {
		t.Fatal("1 should be in flight")
	}
	s.done(1)
	if s.has(1) {
		t.Fatal("1 should be cleared")
	}
}

func TestInflightConcurrent(t *testing.T) {
	s := newInflightSet()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id core.ID) {
			defer wg.Done()
			if s.start(id) {
				s.done(id)
			}
		}(core.ID(i % 10))
	}
	wg.Wait()
	for i := core.ID(0); i < 10; i++ {
		if s.has(i) {
			t.Errorf("id %d leaked", i)
		}
	}
}
