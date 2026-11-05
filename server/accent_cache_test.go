package main

import "testing"

func TestCheckAccentColorCachedReusesResult(t *testing.T) {
	const a, b = "#9c4212", "#1a5fb4"
	before := accentCacheMisses.Load()
	first := checkAccentColorCached(a)
	for range 5 {
		got := checkAccentColorCached(a)
		if got != first {
			t.Fatalf("cached result changed: %+v vs %+v", got, first)
		}
	}
	if misses := accentCacheMisses.Load() - before; misses > 1 {
		t.Errorf("check ran %d times for one accent, want at most 1", misses)
	}
	if want := checkAccentColor(a); first != want {
		t.Errorf("cached = %+v, want %+v", first, want)
	}

	// A different accent (a config change) is computed fresh and matches the uncached check.
	mid := accentCacheMisses.Load()
	changed := checkAccentColorCached(b)
	if accentCacheMisses.Load()-mid > 1 {
		t.Errorf("changed accent recomputed more than once")
	}
	if want := checkAccentColor(b); changed != want {
		t.Errorf("after config change = %+v, want %+v", changed, want)
	}
	if again := checkAccentColorCached(a); again != first {
		t.Errorf("original accent result changed after another was cached")
	}
}

func TestCheckAccentColorCachedInvalidInput(t *testing.T) {
	if got := checkAccentColorCached("nope"); got.OK || got != checkAccentColor("nope") {
		t.Errorf("invalid input = %+v, want the uncached zero result", got)
	}
}
