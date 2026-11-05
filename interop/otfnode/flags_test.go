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
