package pluginrt

import (
	"errors"
	"strings"
	"testing"
)

// TestReloadReappliesThePluginAndItsUsers is the hot reload's guarantee: a
// plugin's Apply runs again without a restart, and every active plugin that
// required a service it provides is disposed after it and applied again
// before it, so no one is left holding a withdrawn service.
func TestReloadReappliesThePluginAndItsUsers(t *testing.T) {
	applies, disposes := map[string]int{}, map[string]int{}
	mk := func(id string, provides []string, requires []string) Manifest {
		return Manifest{
			ID: id, API: "^1", Provides: provides, Requires: requires,
			Apply: func(c *Ctx) error {
				applies[id]++
				if len(provides) == 1 {
					c.Provide(provides[0], applies[id])
				}
				if requires != nil && c.Service("svc") == nil {
					return errors.New("no svc")
				}
				c.OnDispose(func() { disposes[id]++ })
				return nil
			},
		}
	}
	prov, user := mk("prov", []string{"svc"}, nil), mk("user", nil, []string{"svc"})
	rt := New([]Manifest{prov, user})
	rt.Reconcile(map[string]bool{"prov": true, "user": true}, nil)

	if !rt.Active("prov") || !rt.Active("user") {
		t.Fatalf("before the reload: %+v", rt.Statuses())
	}
	if applies["prov"] != 1 || applies["user"] != 1 {
		t.Fatalf("applies before the reload: %v", applies)
	}

	st, err := rt.Reload("prov", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != Active || st.Revision != 3 {
		t.Errorf("after the reload: %+v", st)
	}
	if applies["prov"] != 2 || applies["user"] != 2 {
		t.Errorf("the provider and its user both re-applied: %v", applies)
	}
	if disposes["prov"] != 1 || disposes["user"] != 1 {
		t.Errorf("each disposed once, newest first: %v", disposes)
	}
	// reloading the user alone leaves the provider as it was
	if _, err := rt.Reload("user", nil); err != nil {
		t.Fatal(err)
	}
	if applies["prov"] != 2 {
		t.Errorf("the provider re-applied for the user's reload: %v", applies)
	}
	if applies["user"] != 3 || disposes["user"] != 2 {
		t.Errorf("the user reloaded alone: %v %v", applies, disposes)
	}
}

// TestReloadOffIsRefused: reloading a plugin that is off says so instead of
// quietly applying it, since turning it on is what loads it.
func TestReloadOffIsRefused(t *testing.T) {
	rt := New([]Manifest{{ID: "off", API: "^1", Apply: func(*Ctx) error { return nil }}})
	rt.Reconcile(map[string]bool{"off": false}, nil)
	st, err := rt.Reload("off", nil)
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("reloading an off plugin: %v, %+v", err, st)
	}
	if st.State != Off {
		t.Errorf("state %q, want off", st.State)
	}
}

// TestReloadReportsAFailedApply: a plugin whose Apply fails on the second
// run is Failed with the error, and the runtime keeps running the rest.
func TestReloadReportsAFailedApply(t *testing.T) {
	n := 0
	rt := New([]Manifest{{
		ID: "flaky", API: "^1",
		Apply: func(c *Ctx) error {
			n++
			if n > 1 {
				return errors.New("second run refuses")
			}
			c.OnDispose(func() {})
			return nil
		},
	}})
	rt.Reconcile(map[string]bool{"flaky": true}, nil)
	st, err := rt.Reload("flaky", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != Failed || !strings.Contains(st.Why, "second run refuses") {
		t.Errorf("after the failed reload: %+v", st)
	}
	if st.Revision != 3 {
		t.Errorf("revision %d, want 3 (apply, dispose, apply)", st.Revision)
	}
}
