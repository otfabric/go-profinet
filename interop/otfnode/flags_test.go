// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"io"
	"net"
	"testing"
)

// TestUintFlags: a value beyond the width of the field is a flag error;
// as fs.Uint and a conversion it was truncated (-rr 65544 became 8).
func TestUintFlags(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rr := uint16Flag(fs, "rr", 8, "")
	hello := uint32Flag(fs, "hello", 0, "")
	if *rr != 8 || fs.Lookup("rr").DefValue != "8" || fs.Lookup("hello").DefValue != "0" {
		t.Fatalf("defaults %d %q", *rr, fs.Lookup("rr").DefValue)
	}
	if err := fs.Parse([]string{"-rr", "0x20", "-hello", "4294967295"}); err != nil || *rr != 32 || *hello != 4294967295 {
		t.Fatalf("%d %d %v", *rr, *hello, err)
	}
	for _, args := range [][]string{{"-rr", "65544"}, {"-rr", "-1"}, {"-hello", "4294967297"}, {"-hello", "x"}} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		uint16Flag(fs, "rr", 8, "")
		uint32Flag(fs, "hello", 0, "")
		if err := fs.Parse(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestCheckIface(t *testing.T) {
	for _, bad := range []string{"", "-h", "eth0 up", "a/b", "averyveryverylongname", "eth0;id", "no-such-if9"} {
		if _, err := checkIface(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
		if _, err := ipCommand(bad, "link", "show"); err == nil {
			t.Errorf("ip ran on %q", bad)
		}
	}
	ifs, err := net.Interfaces()
	if err != nil || len(ifs) == 0 {
		t.Skip("no interfaces")
	}
	for _, ifi := range ifs {
		if !ifaceName.MatchString(ifi.Name) {
			continue
		}
		if name, err := checkIface(ifi.Name); err != nil || name != ifi.Name {
			t.Errorf("%q: %q %v", ifi.Name, name, err)
		}
		return
	}
}

// TestParseRecord: an "API@" prefix names the record's API; without one
// it is API 0.
func TestParseRecord(t *testing.T) {
	rec, data, err := parseRecord("0x4E01@1:1:123:0a0b", 4)
	if err != nil || rec.API != 0x4E01 || rec.Slot != 1 || rec.Subslot != 1 || rec.Index != 123 || string(data) != "\x0a\x0b" {
		t.Fatalf("%+v %x %v", rec, data, err)
	}
	if r := recordResult("read", rec); r["api"] != uint32(0x4E01) || r["op"] != "read" || r["index"] != uint16(123) {
		t.Fatalf("%v", r)
	}
	rec, _, err = parseRecord("0:1:0xF821", 3)
	if err != nil || rec.API != 0 || rec.Index != 0xF821 {
		t.Fatalf("%+v %v", rec, err)
	}
	if r := recordResult("", rec); r["api"] != nil || r["op"] != nil {
		t.Fatalf("%v", r)
	}
	for _, bad := range []string{"x@1:1:1", "0x100000000@1:1:1", "1@1:1"} {
		if _, _, err := parseRecord(bad, 3); err == nil {
			t.Fatalf("%q: no error", bad)
		}
	}
}
