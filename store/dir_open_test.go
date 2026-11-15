// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestIsTemp: only the names Save gives its temporary files.
func TestIsTemp(t *testing.T) {
	long := strings.Repeat("a", MaxKeyLen)
	for name, want := range map[string]bool{
		"a.tmp1":              true,
		"a.tmp1234567890":     true,
		"a.tmp.tmp7":          true, // the temporary file of key "a.tmp"
		"x.tmp-1.tmp42":       true,
		long + ".tmp0":        true,
		"a.tmp":               false, // no digits
		"a.tmp1.v":            false, // the value of key "a.tmp1"
		"a.tmp1.v.prev":       false,
		"station.tmpl.v":      false,
		"station.tmpl":        false,
		"a.tmp12x":            false,
		"a.tmp-1":             false,
		".tmp1":               false, // no key
		"A.tmp1":              false, // not a valid key
		"a b.tmp1":            false,
		long + "a.tmp1":       false, // key too long
		"report.tmp.txt":      false,
		"a.v":                 false,
		"":                    false,
		"tmp1":                false,
		"a.tmp١":              false, // digits are ASCII
		"a.tmp1\x00":          false,
		"..tmp1":              false,
		"a.TMP1":              false,
		"a.tmp1 ":             false,
		"a.tmp+1":             false,
		"a.tmp0x10":           false,
		"a.tmp1.tmp":          false,
		"a.tmp1.tmp2":         true, // the temporary file of key "a.tmp1"
		"a.v.tmp3":            true, // the temporary file of key "a.v"
		"a.v.prev.tmp3":       true, // the temporary file of key "a.v.prev"
		"a.v.prev.tmp3.v":     false,
		"a.v.prev.tmp3.v.tmp": false,
	} {
		if got := isTemp(name); got != want {
			t.Errorf("isTemp(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestOpenDirKeepsValues: opening a directory again removes the temporary
// files of its keys and nothing else; a key may contain ".tmp".
func TestOpenDirKeepsValues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "s")
	d, err := OpenDir(path, DirOptions{NoSync: true})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"a", "a.tmp", "a.tmp1", "station.tmpl", "x.tmp-1", "k.tmp123", "name.tmp.v"}
	for _, k := range keys {
		for _, v := range []string{"old " + k, "new " + k} {
			if err := d.Save(ctx, k, []byte(v)); err != nil {
				t.Fatal(err)
			}
		}
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(path, name), []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// What an interrupted Save of each key leaves.
	var temps []string
	for i, k := range keys {
		temps = append(temps, k+".tmp"+strings.Repeat("7", i+1))
		write(temps[i])
	}
	// Files and a directory that are nobody's temporary files.
	others := []string{"report.tmp.txt", "a.tmp", ".tmp1", "A.tmp1", "notes.tmp12.bak"}
	for _, o := range others {
		write(o)
	}
	sub := filepath.Join(path, "sub.tmp9")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	d2, err := OpenDir(path, DirOptions{NoSync: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if v, err := d2.Load(ctx, k); err != nil || string(v) != "new "+k {
			t.Errorf("key %q after a second OpenDir: %q, %v", k, v, err)
		}
		// The previous version is there as well.
		if err := os.Remove(filepath.Join(path, k+".v")); err != nil {
			t.Errorf("key %q: %v", k, err)
		}
		if v, err := d2.Load(ctx, k); err != nil || string(v) != "old "+k {
			t.Errorf("previous version of %q after a second OpenDir: %q, %v", k, v, err)
		}
	}
	for _, name := range temps {
		if _, err := os.Stat(filepath.Join(path, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("temporary file %q left: %v", name, err)
		}
	}
	for _, name := range append(others, "sub.tmp9") {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
}

// TestOpenDirPathIsLiteral: the characters of the path mean themselves;
// the directories next to it are not looked at.
func TestOpenDirPathIsLiteral(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	d1, err := OpenDir(filepath.Join(parent, "dev1"), DirOptions{NoSync: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := d1.Save(ctx, "cfg.tmpl", []byte("neighbour")); err != nil {
		t.Fatal(err)
	}
	kept := []string{"report.tmp.txt", "cfg.tmp5"}
	for _, name := range kept {
		if err := os.WriteFile(filepath.Join(d1.Path(), name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"dev[12]", "dev?", "d*"} {
		d, err := OpenDir(filepath.Join(parent, name), DirOptions{NoSync: true})
		if err != nil {
			// Not every file system takes every name.
			t.Logf("OpenDir(%q): %v", name, err)
			continue
		}
		if err := d.Save(ctx, "k", []byte(name)); err != nil {
			t.Fatal(err)
		}
		if v, err := d.Load(ctx, "k"); err != nil || string(v) != name {
			t.Fatalf("%q: %q, %v", name, v, err)
		}
	}
	if v, err := d1.Load(ctx, "cfg.tmpl"); err != nil || string(v) != "neighbour" {
		t.Fatalf("the neighbouring store: %q, %v", v, err)
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(d1.Path(), name)); err != nil {
			t.Errorf("file %q of the neighbouring directory: %v", name, err)
		}
	}
}

// TestDeleteOrder: Delete removes the previous version first, so a Delete
// that stops after its first step leaves the current value.
func TestDeleteOrder(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("injected")
	orig := remove
	t.Cleanup(func() { remove = orig })
	var recovered int
	d := openDir(t, DirOptions{NoSync: true, OnRecover: func(string, error) { recovered++ }})
	if err := d.Save(ctx, "k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(ctx, "k", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	var removed []string
	remove = func(name string) error {
		if len(removed) == 1 {
			return boom
		}
		removed = append(removed, filepath.Base(name))
		return os.Remove(name)
	}
	if err := d.Delete(ctx, "k"); !errors.Is(err, boom) {
		t.Fatalf("Delete: %v", err)
	}
	remove = orig
	if len(removed) != 1 || removed[0] != "k.v.prev" {
		t.Fatalf("removed first: %v, want k.v.prev", removed)
	}
	if v, err := d.Load(ctx, "k"); err != nil || string(v) != "v2" || recovered != 0 {
		t.Fatalf("after a Delete that stopped half-way: %q, %v (recovered %d), want the current value v2", v, err, recovered)
	}
	if err := d.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Load(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete: %v", err)
	}
}

// TestOnRecoverMayUseTheDir: OnRecover runs with no lock of the Dir held:
// it may load, save and delete (contract rows L9 and L10 for a callback
// of a type without Close).
func TestOnRecoverMayUseTheDir(t *testing.T) {
	ctx := context.Background()
	var d *Dir
	var calls int
	var inner []string
	d = openDir(t, DirOptions{NoSync: true, OnRecover: func(key string, _ error) {
		calls++
		if calls > 1 {
			return
		}
		// A getter, a write of another key, and the repair of this one.
		v, err := d.Load(ctx, "other")
		inner = append(inner, string(v), errString(err))
		inner = append(inner, errString(d.Save(ctx, "seen", []byte(key))))
		inner = append(inner, errString(d.Delete(ctx, "other")))
		inner = append(inner, errString(d.Save(ctx, key, []byte("repaired"))))
	}})
	for _, v := range []string{"v1", "v2"} {
		if err := d.Save(ctx, "k", []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Save(ctx, "other", []byte("o")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d.Path(), "k.v")); err != nil {
		t.Fatal(err)
	}
	type result struct {
		v   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := d.Load(ctx, "k")
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || string(r.v) != "v1" {
			t.Fatalf("Load: %q, %v", r.v, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Load did not return: OnRecover could not use the Dir")
	}
	if want := []string{"o", "", "", "", ""}; calls != 1 || !slices.Equal(inner, want) {
		t.Fatalf("OnRecover ran %d times with results %q, want once with %q", calls, inner, want)
	}
	if v, err := d.Load(ctx, "k"); err != nil || string(v) != "repaired" {
		t.Fatalf("after the repair: %q, %v", v, err)
	}
	if v, err := d.Load(ctx, "seen"); err != nil || string(v) != "k" {
		t.Fatalf("the key OnRecover saved: %q, %v", v, err)
	}
	if _, err := d.Load(ctx, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the key OnRecover deleted: %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
