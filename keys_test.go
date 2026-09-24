package main

import "testing"

// A key bound to two actions would silently shadow one of them.
func TestNoDuplicateKeys(t *testing.T) {
	seen := map[string]action{}
	for _, g := range bindingGroups {
		for _, b := range g.bindings {
			for _, k := range b.keys {
				if prev, dup := seen[k]; dup && prev != b.act {
					t.Errorf("key %q bound to both action %d and %d", k, prev, b.act)
				}
				seen[k] = b.act
			}
		}
	}
}
