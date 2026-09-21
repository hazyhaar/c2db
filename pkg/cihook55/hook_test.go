package cihook55

import "testing"

func TestFireInvokesAndClears(t *testing.T) {
	n := 0
	Set("x", func() { n++ })
	Fire("x")
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	Set("x", nil)
	Fire("x")
	if n != 1 {
		t.Fatalf("after clear n=%d", n)
	}
}
