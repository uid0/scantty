//go:build omslab

package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Drive every read method the client exposes that needs nothing but a context
// (and optionally a query) against a REAL OMS, and fail on any that does not
// decode.
//
// Build-tagged, so it is inert without a backend. Run it as:
//
//	OMS_LAB_URL=http://127.0.0.1:8123 OMS_LAB_TOKEN=$(…) \
//	  go test -tags omslab -count=1 -run TestLabSweep ./internal/omsapi/ -v
//
// Bringing that backend up is in AGENTS.md ("Verifying against a REAL OMS").
// Use -count=1: the test cache keys on env vars and will replay a stale PASS
// after the backend changes.
//
// The set is DERIVED by reflecting over *Client rather than listed, so a method
// added later is swept without anyone remembering it. It was written to answer
// one question — `decodeBody` sets UseNumber, which changes what every `any` on
// every payload holds, and a hand-picked list of endpoints would only prove
// that about the ones somebody thought of.
//
// WHAT IT DOES NOT PROVE, said plainly because a green run here looks like more
// than it is:
//
//   - An EMPTY list decodes into ANY element type, so a method whose reply
//     carried no rows says nothing about its element's fields. The run reports
//     which calls carried rows and which came back empty; on a lab seeded only
//     with purchasing data that was 16 against 37. Seed the area under
//     suspicion before trusting it about that area.
//   - Only methods taking (ctx) or (ctx, url.Values) are reachable this way, so
//     everything addressed by an id is outside it — LookupPurchaseOrderLine,
//     the very endpoint whose type mismatch prompted all this, takes three
//     arguments and is NOT in this set. Its own recorded-payload tests are in
//     po_line_entry_wire_test.go.
func TestLabSweep_EveryNoArgReadDecodes(t *testing.T) {
	c := New(os.Getenv("OMS_LAB_URL"), WithToken(os.Getenv("OMS_LAB_TOKEN"), ""))
	ctx := context.Background()

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	valsType := reflect.TypeOf(url.Values(nil))
	errType := reflect.TypeOf((*error)(nil)).Elem()

	cv := reflect.ValueOf(c)
	var names []string
	methods := map[string]reflect.Value{}
	for i := 0; i < cv.NumMethod(); i++ {
		m := cv.Type().Method(i)
		if !strings.HasPrefix(m.Name, "List") && !strings.HasPrefix(m.Name, "Get") {
			continue
		}
		ft := m.Type // includes the receiver at index 0
		if ft.NumOut() != 2 || !ft.Out(1).Implements(errType) {
			continue
		}
		var args []reflect.Value
		switch {
		case ft.NumIn() == 2 && ft.In(1) == ctxType:
			args = []reflect.Value{reflect.ValueOf(ctx)}
		case ft.NumIn() == 3 && ft.In(1) == ctxType && ft.In(2) == valsType:
			args = []reflect.Value{reflect.ValueOf(ctx), reflect.Zero(valsType)}
		default:
			continue
		}
		names = append(names, m.Name)
		methods[m.Name] = cv.Method(i)
		_ = args
	}
	sort.Strings(names)
	if len(names) < 20 {
		t.Fatalf("only %d methods swept — the derivation found almost nothing, so a "+
			"pass here would mean nothing", len(names))
	}
	t.Logf("sweeping %d no-argument read methods", len(names))

	// Two endpoints need Redis, which this lab does not run: both fail with a
	// Django 500 / a timeout carrying redis.exceptions.TimeoutError, neither of
	// which is a decode. Recorded WITH the reason so "excluded" and "passed"
	// stay different states, and the sweep still fails if one starts failing
	// for a reason that IS a decode.
	needsRedis := map[string]bool{
		"GetResilienceStatus":          true,
		"ListProjectStoragePrintQueue": true,
	}

	var failed, excused, withRows, empty []string
	for _, name := range names {
		m := methods[name]
		ft := m.Type()
		args := []reflect.Value{reflect.ValueOf(ctx)}
		if ft.NumIn() == 2 {
			args = append(args, reflect.Zero(valsType))
		}
		out := m.Call(args)
		e := out[1].Interface()
		if e == nil {
			if needsRedis[name] {
				t.Errorf("%s is excused as needing Redis but decoded — stale exception", name)
			}
			if n := payloadRows(out[0]); n > 0 {
				withRows = append(withRows, fmt.Sprintf("%s(%d)", name, n))
			} else {
				empty = append(empty, name)
			}
			continue
		}
		msg := e.(error).Error()
		if needsRedis[name] && !strings.Contains(msg, "cannot unmarshal") &&
			!strings.Contains(msg, "decode response") {
			excused = append(excused, name)
			continue
		}
		failed = append(failed, name+": "+msg)
	}
	for _, x := range excused {
		t.Logf("excused (this lab runs no Redis): %s", x)
	}
	for _, f := range failed {
		t.Logf("FAILED %s", f)
	}
	// An EMPTY list decodes into any element type, so it proves nothing about
	// the element's fields. Say which calls actually carried rows rather than
	// letting a green sweep imply coverage it does not have.
	sort.Strings(withRows)
	sort.Strings(empty)
	t.Logf("%d/%d decoded, %d excused", len(names)-len(failed)-len(excused), len(names), len(excused))
	t.Logf("carried rows (%d): %s", len(withRows), strings.Join(withRows, " "))
	t.Logf("came back EMPTY, so their element types are untested (%d): %s",
		len(empty), strings.Join(empty, " "))
	if len(failed) > 0 {
		t.Errorf("%d of %d read endpoints did not decode", len(failed), len(names))
	}
}

// payloadRows reports how many elements a decoded reply carried, so an empty
// answer is not mistaken for an exercised one. -1 when the shape has no count.
func payloadRows(v reflect.Value) int {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len()
	case reflect.Struct:
		// Page[T] and the envelope shapes: count the biggest slice they hold.
		best := -1
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.Kind() == reflect.Slice && f.Len() > best {
				best = f.Len()
			}
		}
		if best >= 0 {
			return best
		}
		return 1
	}
	return 1
}
