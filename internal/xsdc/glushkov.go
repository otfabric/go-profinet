// SPDX-License-Identifier: MIT

package xsdc

import (
	"fmt"
	"sort"
)

// Position is a leaf of a content model in its Glushkov automaton: an
// element particle or a wildcard.
type Position struct {
	Element  *Element
	Wildcard *Wildcard
}

// DFA is the Glushkov automaton of a content model. State 0 is the start;
// state i > 0 is "just matched Positions[i-1]". XML Schema's Unique
// Particle Attribution constraint makes it deterministic, which Compile
// checks.
type DFA struct {
	Positions []Position
	Final     []bool
	// Next[s] lists the positions (as states, 1-based) reachable from s
	// by one element, in content-model order.
	Next [][]int
}

type gnode struct {
	kind     ParticleKind
	children []*gnode
	pos      int // ElementParticle, AnyParticle: 1-based position
	star     bool
	opt      bool
}

// Compile builds the automaton of a content model. Occurrence bounds
// other than 0, 1 and unbounded are expanded into copies.
func Compile(p *Particle) (*DFA, error) {
	d := &DFA{}
	var build func(p *Particle) (*gnode, error)
	leaf := func(p *Particle) *gnode {
		d.Positions = append(d.Positions, Position{Element: p.Element, Wildcard: p.Wildcard})
		return &gnode{kind: p.Kind, pos: len(d.Positions)}
	}
	term := func(p *Particle) (*gnode, error) {
		switch p.Kind {
		case ElementParticle, AnyParticle:
			return leaf(p), nil
		case All:
			return nil, fmt.Errorf("xsd:all is not used by GSDML and not implemented")
		default:
		}
		g := &gnode{kind: p.Kind}
		for _, c := range p.Children {
			x, err := build(c)
			if err != nil {
				return nil, err
			}
			g.children = append(g.children, x)
		}
		return g, nil
	}
	build = func(p *Particle) (*gnode, error) {
		switch {
		case p.Max == 0:
			return &gnode{kind: Sequence}, nil
		case p.Min <= 1 && (p.Max == 1 || p.Max == Unbounded):
			g, err := term(p)
			if err != nil {
				return nil, err
			}
			if p.Max == Unbounded || p.Min == 0 {
				g = &gnode{kind: Sequence, children: []*gnode{g}, star: p.Max == Unbounded, opt: p.Min == 0}
			}
			return g, nil
		}
		// expand: min copies, then (max-min) optional copies or a star
		seq := &gnode{kind: Sequence}
		for i := 0; i < p.Min; i++ {
			g, err := term(p)
			if err != nil {
				return nil, err
			}
			seq.children = append(seq.children, g)
		}
		if p.Max == Unbounded {
			g, err := term(p)
			if err != nil {
				return nil, err
			}
			seq.children = append(seq.children, &gnode{kind: Sequence, children: []*gnode{g}, star: true, opt: true})
		} else {
			for i := p.Min; i < p.Max; i++ {
				g, err := term(p)
				if err != nil {
					return nil, err
				}
				seq.children = append(seq.children, &gnode{kind: Sequence, children: []*gnode{g}, opt: true})
			}
		}
		return seq, nil
	}
	root, err := build(p)
	if err != nil {
		return nil, err
	}
	n := len(d.Positions)
	follow := make([]map[int]bool, n+1)
	for i := range follow {
		follow[i] = map[int]bool{}
	}
	var first, last func(g *gnode) []int
	var nullable func(g *gnode) bool
	nullable = func(g *gnode) bool {
		if g.opt || g.star {
			if g.opt {
				return true
			}
		}
		switch g.kind {
		case ElementParticle, AnyParticle:
			return false
		case Choice:
			if len(g.children) == 0 {
				return false
			}
			for _, c := range g.children {
				if nullable(c) {
					return true
				}
			}
			return false
		default:
		}
		for _, c := range g.children {
			if !nullable(c) {
				return false
			}
		}
		return true
	}
	first = func(g *gnode) []int {
		switch g.kind {
		case ElementParticle, AnyParticle:
			return []int{g.pos}
		case Choice:
			var out []int
			for _, c := range g.children {
				out = append(out, first(c)...)
			}
			return out
		default:
		}
		var out []int
		for _, c := range g.children {
			out = append(out, first(c)...)
			if !nullable(c) {
				break
			}
		}
		return out
	}
	last = func(g *gnode) []int {
		switch g.kind {
		case ElementParticle, AnyParticle:
			return []int{g.pos}
		case Choice:
			var out []int
			for _, c := range g.children {
				out = append(out, last(c)...)
			}
			return out
		default:
		}
		var out []int
		for i := len(g.children) - 1; i >= 0; i-- {
			out = append(out, last(g.children[i])...)
			if !nullable(g.children[i]) {
				break
			}
		}
		return out
	}
	var fill func(g *gnode)
	fill = func(g *gnode) {
		for _, c := range g.children {
			fill(c)
		}
		if g.kind == Sequence {
			for i := 0; i+1 < len(g.children); i++ {
				for j := i + 1; j < len(g.children); j++ {
					for _, l := range last(g.children[i]) {
						for _, f := range first(g.children[j]) {
							follow[l][f] = true
						}
					}
					if !nullable(g.children[j]) {
						break
					}
				}
			}
		}
		if g.star {
			for _, l := range last(g) {
				for _, f := range first(g) {
					follow[l][f] = true
				}
			}
		}
	}
	fill(root)
	d.Final = make([]bool, n+1)
	d.Next = make([][]int, n+1)
	d.Final[0] = nullable(root)
	d.Next[0] = uniq(first(root))
	for _, l := range last(root) {
		d.Final[l] = true
	}
	for i := 1; i <= n; i++ {
		var next []int
		for f := range follow[i] {
			next = append(next, f)
		}
		sort.Ints(next)
		d.Next[i] = next
	}
	if err := d.checkUPA(); err != nil {
		return nil, err
	}
	return d, nil
}

func uniq(x []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range x {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (d *DFA) names(p int) []QName {
	pos := d.Positions[p-1]
	if pos.Element == nil {
		return nil
	}
	out := []QName{pos.Element.Name}
	for _, s := range pos.Element.Substitutes {
		out = append(out, s.Name)
	}
	return out
}

// checkUPA rejects a state with two transitions that can match the same
// element.
func (d *DFA) checkUPA() error {
	for s, next := range d.Next {
		for i := 0; i < len(next); i++ {
			for j := i + 1; j < len(next); j++ {
				a, b := d.Positions[next[i]-1], d.Positions[next[j]-1]
				if overlap(a, b, d.names(next[i]), d.names(next[j])) {
					return fmt.Errorf("content model is not deterministic in state %d (Unique Particle Attribution)", s)
				}
			}
		}
	}
	return nil
}

func overlap(a, b Position, na, nb []QName) bool {
	switch {
	case a.Wildcard != nil && b.Wildcard != nil:
		return wildOverlap(a.Wildcard, b.Wildcard)
	case a.Wildcard != nil:
		for _, n := range nb {
			if a.Wildcard.Allows(n.NS) {
				return true
			}
		}
		return false
	case b.Wildcard != nil:
		for _, n := range na {
			if b.Wildcard.Allows(n.NS) {
				return true
			}
		}
		return false
	}
	for _, x := range na {
		for _, y := range nb {
			if x == y {
				return true
			}
		}
	}
	return false
}

func wildOverlap(a, b *Wildcard) bool {
	if a.Any || b.Any {
		return true
	}
	cand := append(append([]string{"", a.Target, b.Target, "urn:other"}, a.Allowed...), b.Allowed...)
	for _, ns := range cand {
		if a.Allows(ns) && b.Allows(ns) {
			return true
		}
	}
	return false
}

// Order reports whether two different element names (or an element and a
// wildcard) can occur in either order in a valid content: then an
// encoder needs the document order to reproduce it.
func (d *DFA) Order() bool {
	n := len(d.Positions)
	key := func(p int) string {
		pos := d.Positions[p-1]
		if pos.Wildcard != nil {
			return "*"
		}
		return pos.Element.Name.String()
	}
	// reach[p]: positions reachable after p (transitively)
	after := map[string]map[string]bool{}
	for p := 1; p <= n; p++ {
		seen := map[int]bool{}
		stack := append([]int(nil), d.Next[p]...)
		for len(stack) > 0 {
			q := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[q] {
				continue
			}
			seen[q] = true
			stack = append(stack, d.Next[q]...)
		}
		kp := key(p)
		for q := range seen {
			kq := key(q)
			if kq == kp {
				continue
			}
			if after[kp] == nil {
				after[kp] = map[string]bool{}
			}
			after[kp][kq] = true
		}
	}
	for a, m := range after {
		for b := range m {
			if after[b][a] {
				return true
			}
		}
	}
	return false
}
