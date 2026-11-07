//go:build windows

package main

import "testing"

func TestColumnsOf(t *testing.T) {
	// The popup's list at 100% scaling: 360 px wide, 10 px padding, 8 px gaps.
	got := columnsOf(360, 10, 8)
	want := [3]span{{10, 131}, {139, 240}, {248, 350}}
	if got != want {
		t.Errorf("columnsOf(360, 10, 8) = %v, want %v", got, want)
	}
	for _, check := range []struct{ x, want int }{
		{0, 0}, {130, 0}, {135, loginColumn}, {239, loginColumn}, {245, passwordColumn}, {359, passwordColumn},
	} {
		if column := columnAt(check.x, got); column != check.want {
			t.Errorf("columnAt(%d) = %d, want %d", check.x, column, check.want)
		}
	}
	// A row too narrow for its padding must not produce negative widths.
	for i, c := range columnsOf(20, 10, 8) {
		if c.end < c.start {
			t.Errorf("narrow row: column %d is %v", i, c)
		}
	}
}
