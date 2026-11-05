// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
)

// uint16Value is a flag.Value of an unsigned 16-bit integer: a value that
// does not fit is a flag error, not truncated.
type uint16Value uint16

func (v *uint16Value) String() string { return strconv.FormatUint(uint64(*v), 10) }

func (v *uint16Value) Set(s string) error {
	n, err := strconv.ParseUint(s, 0, 16)
	if err != nil {
		return err
	}
	*v = uint16Value(n)
	return nil
}

// uint16Flag defines a uint16 flag.
func uint16Flag(fs *flag.FlagSet, name string, def uint16, usage string) *uint16 {
	v := def
	fs.Var((*uint16Value)(&v), name, usage)
	return &v
}

// uint32Value is a flag.Value of an unsigned 32-bit integer.
type uint32Value uint32

func (v *uint32Value) String() string { return strconv.FormatUint(uint64(*v), 10) }

func (v *uint32Value) Set(s string) error {
	n, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		return err
	}
	*v = uint32Value(n)
	return nil
}

// uint32Flag defines a uint32 flag.
func uint32Flag(fs *flag.FlagSet, name string, def uint32, usage string) *uint32 {
	v := def
	fs.Var((*uint32Value)(&v), name, usage)
	return &v
}

// ifaceName is what an interface name may be on Linux (at most IFNAMSIZ-1
// octets, no slash or white space); otfnode allows the conservative
// subset that interface names in containers use, so that a name passed to
// ip(8) is never an option or more than one argument.
var ifaceName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.@:-]{0,14}$`)

// checkIface returns the name of an existing interface, or an error for a
// name that is not one.
func checkIface(name string) (string, error) {
	if !ifaceName.MatchString(name) {
		return "", fmt.Errorf("interface name %q not allowed", name)
	}
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return "", err
	}
	return ifi.Name, nil
}

// ipCommand runs ip(8) on an existing interface.
func ipCommand(iface string, args ...string) ([]byte, error) {
	name, err := checkIface(iface)
	if err != nil {
		return nil, err
	}
	argv := make([]string, 0, len(args)+2)
	argv = append(argv, args...)
	argv = append(argv, "dev", name)
	return exec.Command("ip", argv...).CombinedOutput()
}
