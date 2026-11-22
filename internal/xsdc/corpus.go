// SPDX-License-Identifier: MIT

package xsdc

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var deviceProfile = regexp.MustCompile(`^GSDML-DeviceProfile-[vV]([0-9]+\.[0-9]+)\.xsd$`)

// VersionNumber orders GSDML versions: major, then the minor as two
// digits, so that 2.4 (2.40) follows 2.35.
func VersionNumber(v string) int {
	major, minor, _ := strings.Cut(v, ".")
	for len(minor) < 2 {
		minor += "0"
	}
	a, _ := strconv.Atoi(major)
	b, _ := strconv.Atoi(minor[:2])
	return a*100 + b
}

// LoadCorpus loads every GSDML-DeviceProfile-vX.Y.xsd in dir, the version
// taken from the file name, oldest first.
func LoadCorpus(dir string) (*Corpus, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type vf struct{ v, f string }
	var vs []vf
	for _, e := range ents {
		if m := deviceProfile.FindStringSubmatch(e.Name()); m != nil {
			vs = append(vs, vf{m[1], e.Name()})
		}
	}
	if len(vs) == 0 {
		return nil, fmt.Errorf("xsdc: no GSDML-DeviceProfile schema in %s", dir)
	}
	sort.Slice(vs, func(i, j int) bool { return VersionNumber(vs[i].v) < VersionNumber(vs[j].v) })
	c := &Corpus{}
	for i, x := range vs {
		if i > 0 && VersionNumber(vs[i-1].v) == VersionNumber(x.v) {
			return nil, fmt.Errorf("xsdc: two schemas for version %s", x.v)
		}
		s, err := Load(dir, x.f)
		if err != nil {
			return nil, fmt.Errorf("xsdc: %s: %w", x.f, err)
		}
		c.Versions = append(c.Versions, x.v)
		c.Sets = append(c.Sets, s)
	}
	return c, nil
}
