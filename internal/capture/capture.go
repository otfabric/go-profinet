// SPDX-License-Identifier: MIT

// Package capture reads the recorded PROFINET traffic in testdata/captures
// for tests: hex fixtures (one Ethernet frame per file, "#" starts a
// comment), classic pcap files, and the manifest that records where each
// file came from. It splits a frame into what go-profinet's codecs take:
// the Ethernet header (with or without an 802.1Q tag), and for IPv4/UDP
// frames the UDP payload with its addresses.
//
// It is test infrastructure and not part of the public API. It reads files
// from this repository, not untrusted input, but it still checks every
// length it reads.
package capture

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/otfabric/go-profinet/ethernet"
	"github.com/otfabric/go-profinet/pcap"
)

// ErrFormat reports a fixture or frame that is not what the reader expects.
var ErrFormat = errors.New("capture: malformed fixture")

// Dir returns the absolute path of testdata/captures.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "captures")
}

// Path returns the absolute path of a file below testdata/captures.
func Path(rel string) string { return filepath.Join(Dir(), filepath.FromSlash(rel)) }

func readRepoFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path))
}

// Hex is one hex fixture: the frame and the comment lines above it.
type Hex struct {
	Comment string
	Frame   []byte
}

// ReadHex reads a hex fixture below testdata/captures.
func ReadHex(rel string) (Hex, error) {
	b, err := readRepoFile(Path(rel))
	if err != nil {
		return Hex{}, err
	}
	return ParseHex(string(b))
}

// ParseHex parses the text of a hex fixture: everything after a "#" on a
// line is a comment, whitespace is ignored.
func ParseHex(s string) (Hex, error) {
	var h Hex
	var digits strings.Builder
	for line := range strings.Lines(s) {
		code, comment, found := strings.Cut(line, "#")
		if found {
			if h.Comment != "" {
				h.Comment += "\n"
			}
			h.Comment += strings.TrimSpace(comment)
		}
		for _, r := range code {
			if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
				digits.WriteRune(r)
			}
		}
	}
	b, err := hex.DecodeString(digits.String())
	if err != nil {
		return Hex{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if len(b) == 0 {
		return Hex{}, fmt.Errorf("%w: no frame", ErrFormat)
	}
	h.Frame = b
	return h, nil
}

// MustHex is ReadHex for tests: it panics on error.
func MustHex(rel string) []byte {
	h, err := ReadHex(rel)
	if err != nil {
		panic(err)
	}
	return h.Frame
}

// ReadPcap returns the Ethernet frames of a classic pcap or a pcapng file
// below testdata/captures (or at an absolute path), in file order.
func ReadPcap(path string) ([][]byte, error) {
	if !filepath.IsAbs(path) {
		path = Path(path)
	}
	b, err := readRepoFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) >= 4 && binary.LittleEndian.Uint32(b) == 0x0A0D0D0A {
		return readPcapng(b)
	}
	r, err := pcap.NewReader(strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for {
		_, f, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
}

// readPcapng returns the packets of the Enhanced Packet Blocks of a pcapng
// file whose interfaces are all Ethernet.
func readPcapng(b []byte) ([][]byte, error) {
	var out [][]byte
	var o binary.ByteOrder = binary.LittleEndian
	for len(b) > 0 {
		if len(b) < 12 {
			return nil, fmt.Errorf("%w: pcapng block cut short", ErrFormat)
		}
		typ := binary.LittleEndian.Uint32(b)
		if typ == 0x0A0D0D0A { // section header: its byte-order magic
			if binary.LittleEndian.Uint32(b[8:]) != 0x1A2B3C4D {
				o = binary.BigEndian
			} else {
				o = binary.LittleEndian
			}
		} else {
			typ = o.Uint32(b)
		}
		n := int(o.Uint32(b[4:]))
		if n < 12 || n%4 != 0 || n > len(b) {
			return nil, fmt.Errorf("%w: pcapng block of %d octets", ErrFormat, n)
		}
		body := b[8 : n-4]
		switch typ {
		case 1: // interface description
			if len(body) < 2 || o.Uint16(body) != 1 {
				return nil, fmt.Errorf("%w: pcapng interface is not Ethernet", ErrFormat)
			}
		case 6: // enhanced packet
			if len(body) < 20 {
				return nil, fmt.Errorf("%w: pcapng packet block cut short", ErrFormat)
			}
			c := int(o.Uint32(body[12:]))
			if c > len(body)-20 {
				return nil, fmt.Errorf("%w: pcapng packet of %d octets", ErrFormat, c)
			}
			out = append(out, append([]byte(nil), body[20:20+c]...))
		}
		b = b[n:]
	}
	return out, nil
}

// UDP is the UDP datagram of an IPv4 frame.
type UDP struct {
	Eth      ethernet.Frame
	Src, Dst netip.AddrPort
	Payload  []byte
}

// DecodeUDP splits an Ethernet frame carrying IPv4 and UDP. The IP
// header's total length strips the Ethernet padding; options are skipped.
func DecodeUDP(frame []byte) (UDP, error) {
	var u UDP
	f, err := ethernet.Decode(frame)
	if err != nil {
		return u, err
	}
	u.Eth = f
	if f.EtherType != ethernet.EtherTypeIPv4 {
		return u, fmt.Errorf("%w: EtherType %s is not IPv4", ErrFormat, f.EtherType)
	}
	p := f.Payload
	if len(p) < 20 || p[0]>>4 != 4 {
		return u, fmt.Errorf("%w: not an IPv4 header", ErrFormat)
	}
	ihl := int(p[0]&0x0F) * 4
	total := int(binary.BigEndian.Uint16(p[2:]))
	if ihl < 20 || total < ihl+8 || total > len(p) {
		return u, fmt.Errorf("%w: IPv4 lengths %d/%d of %d octets", ErrFormat, ihl, total, len(p))
	}
	if p[9] != 17 {
		return u, fmt.Errorf("%w: IP protocol %d is not UDP", ErrFormat, p[9])
	}
	src, _ := netip.AddrFromSlice(p[12:16])
	dst, _ := netip.AddrFromSlice(p[16:20])
	udp := p[ihl:total]
	ulen := int(binary.BigEndian.Uint16(udp[4:]))
	if ulen < 8 || ulen > len(udp) {
		return u, fmt.Errorf("%w: UDP length %d of %d octets", ErrFormat, ulen, len(udp))
	}
	u.Src = netip.AddrPortFrom(src, binary.BigEndian.Uint16(udp))
	u.Dst = netip.AddrPortFrom(dst, binary.BigEndian.Uint16(udp[2:]))
	u.Payload = udp[8:ulen]
	return u, nil
}

// Manifest is testdata/captures/manifest.json.
type Manifest struct {
	Sources []Source `json:"sources"`
	Files   []File   `json:"files"`
}

// Source is where a set of files came from.
type Source struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	Revision     string `json:"revision"`
	Acquired     string `json:"acquired"`
	License      string `json:"license"`
	Redistribute string `json:"redistribution"`
	Class        string `json:"class"`
	Hardware     string `json:"hardware"`
	Peers        string `json:"peers"`
	Limitations  string `json:"limitations"`
}

// File is one committed fixture or one raw file that is only referenced.
type File struct {
	Path      string `json:"path"`
	Source    string `json:"source"`
	Original  string `json:"original"`
	Frames    string `json:"frames,omitempty"`
	Direction string `json:"direction,omitempty"`
	SHA256    string `json:"sha256"`
	Size      int    `json:"size"`
	Committed bool   `json:"committed"`
	Class     string `json:"class"`
	What      string `json:"what"`
}

// ReadManifest reads testdata/captures/manifest.json.
func ReadManifest() (Manifest, error) {
	var m Manifest
	b, err := readRepoFile(Path("manifest.json"))
	if err != nil {
		return m, err
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, fmt.Errorf("%w: manifest: %v", ErrFormat, err)
	}
	return m, nil
}
