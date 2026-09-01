package tui

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func TestScratchCounters(t *testing.T) {
	tokens := map[string]bool{}
	for tok := range jdeMoveTokens {
		tokens[tok] = true
	}
	keyTokens := map[string][]string{}
	for token := range tokens {
		for _, k := range jdeMoveTokens[token] {
			keyTokens[k] = append(keyTokens[k], token)
		}
	}
	named := map[string]int{}
	unnamed := map[string]int{}
	moves := map[string]int{}
	drawn := 0
	for _, c := range jdePaneCases() {
		mk := c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				jdeRootAt(t, s, w, h)
				bar := jdeBarOf(s.View())
				if bar == nil {
					continue
				}
				drawn++
				on := map[string]bool{}
				for _, tok := range jdeBarTokens(bar) {
					on[tok] = true
				}
				for token := range tokens {
					if on[token] {
						named[token]++
					} else {
						unnamed[token]++
					}
				}
				for key := range keyTokens {
					probe := mk()
					jdeRootAt(t, probe, w, h)
					before := jdePlaceOf(probe)
					if next, _ := probe.Update(poPickerKeyMsg(key)); next != nil {
						probe = next
					}
					if !reflect.DeepEqual(before, jdePlaceOf(probe)) {
						moves[key]++
					}
				}
			}
		}
	}
	fmt.Printf("drawn=%d\n", drawn)
	toks := []string{}
	for tok := range tokens {
		toks = append(toks, tok)
	}
	sort.Strings(toks)
	for _, tok := range toks {
		fmt.Printf("token %-12s named=%-6d unnamed=%d\n", tok, named[tok], unnamed[tok])
	}
	keys := []string{}
	for k := range keyTokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("key   %-12s moves=%d\n", k, moves[k])
	}
}
