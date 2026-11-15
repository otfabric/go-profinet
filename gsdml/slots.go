// SPDX-License-Identifier: MIT

package gsdml

import (
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

// The slot rules work on the slot numbers a PROFINET slot number can
// take, 0 to 65535, in fixed-size sets: what a slot list costs is its
// text and a fixed number of words, not the values of its ranges.
const (
	slotNumbers = 1 << 16
	slotWords   = slotNumbers / 64
	// maxSlotValues is the number of values a slot list may have to be
	// looked at, as before: a longer list is left out.
	maxSlotValues = 1 << 16
)

// slotSet is the set of the slot numbers of one slot list.
type slotSet struct {
	bits [slotWords]uint64
	// lo and hi are the first and the last word in use; every other word
	// is zero. An empty set has lo > hi.
	lo, hi int
	// beyond is the number of values of the list above the slot numbers.
	beyond uint64
}

func (s *slotSet) reset() {
	for w := s.lo; w <= s.hi; w++ {
		s.bits[w] = 0
	}
	s.lo, s.hi, s.beyond = slotWords, -1, 0
}

// parse makes s the set of a slot list. It accepts what
// ParseValueList(list, maxSlotValues) accepts, without building the
// values: ok is false for an empty list, a text that is no value list and
// a list of more than maxSlotValues values, and s is then undefined.
func (s *slotSet) parse(list string) (ok bool) {
	s.reset()
	if strings.TrimSpace(list) == "" {
		return false
	}
	var n uint64
	for tok := range strings.FieldsSeq(list) {
		lo, hi := tok, tok
		if before, after, cut := strings.Cut(tok, ".."); cut {
			lo, hi = before, after
		}
		a, err := strconv.ParseUint(lo, 10, 32)
		if err != nil {
			return false
		}
		b, err := strconv.ParseUint(hi, 10, 32)
		if err != nil || b < a {
			return false
		}
		if n += b - a + 1; n > maxSlotValues {
			return false
		}
		s.add(a, b)
	}
	return true
}

// add adds the values a to b.
func (s *slotSet) add(a, b uint64) {
	if a >= slotNumbers {
		s.beyond += b - a + 1
		return
	}
	if b >= slotNumbers {
		s.beyond += b - (slotNumbers - 1)
		b = slotNumbers - 1
	}
	wa, wb := int(a>>6), int(b>>6)
	first, last := ^uint64(0)<<(a&63), ^uint64(0)>>(63-b&63)
	if wa == wb {
		s.bits[wa] |= first & last
	} else {
		s.bits[wa] |= first
		for w := wa + 1; w < wb; w++ {
			s.bits[w] = ^uint64(0)
		}
		s.bits[wb] |= last
	}
	s.lo, s.hi = min(s.lo, wa), max(s.hi, wb)
}

// missing returns the slots of s that other does not have: the first
// maxSlotWarnings of them in ascending order, appended to first, and
// their number.
func (s *slotSet) missing(other *slotSet, first []uint32) ([]uint32, int) {
	total := 0
	for w := s.lo; w <= s.hi; w++ {
		x := s.bits[w] &^ other.bits[w]
		if x == 0 {
			continue
		}
		total += bits.OnesCount64(x)
		for ; x != 0 && len(first) < maxSlotWarnings; x &= x - 1 {
			first = append(first, uint32(w<<6|bits.TrailingZeros64(x)))
		}
	}
	return first, total
}

// ranges calls fn for every run of consecutive slots of s, in ascending
// order.
func (s *slotSet) ranges(fn func(lo, hi uint32)) {
	open := false
	var start, end uint32
	for w := s.lo; w <= s.hi; w++ {
		x, base := s.bits[w], uint32(w)<<6
		for bit := uint32(0); bit < 64 && x>>bit != 0; {
			bit += uint32(bits.TrailingZeros64(x >> bit))
			// The run that starts at bit; the shift filled the upper
			// bits with zeros, so it ends within the word.
			ones := uint32(bits.TrailingZeros64(^(x >> bit)))
			a, b := base+bit, base+bit+ones-1
			switch {
			case open && a == end+1:
				end = b
			case open:
				fn(start, end)
				start, end = a, b
			default:
				start, end, open = a, b, true
			}
			bit += ones
		}
	}
	if open {
		fn(start, end)
	}
}

// fixedSlots says which module fixed each slot of a device access point.
// The slots are kept in pages: a page whose slots have one owner, or
// none, is that owner alone, so that fixing a range of slots costs its
// pages and the slots of at most the two pages at its ends.
type fixedSlots struct {
	page [slotNumbers / pageSlots]int
	// slot has the owner of every slot of the pages marked pageMixed. It
	// is made when the first page needs it.
	slot []int
}

const (
	pageSlots = 256
	// noOwner is the owner of a slot no module fixed.
	noOwner = -1
	// pageMixed marks a page whose slots are in fixedSlots.slot.
	pageMixed = -2
)

func (f *fixedSlots) reset() {
	for i := range f.page {
		f.page[i] = noOwner
	}
}

// fix makes owner the owner of the slots lo to hi, and calls taken for
// every run of them another owner had, in ascending order.
func (f *fixedSlots) fix(lo, hi uint32, owner int, taken func(lo, hi uint32, prev int)) {
	for lo <= hi {
		p := lo / pageSlots
		first, last := p*pageSlots, p*pageSlots+pageSlots-1
		end := min(hi, last)
		whole := lo == first && end == last
		prev := f.page[p]
		switch {
		case prev != pageMixed && whole:
			if prev != noOwner && prev != owner {
				taken(lo, end, prev)
			}
			f.page[p] = owner
		case prev == owner:
			// The page is the owner's already.
		default:
			if prev != pageMixed {
				if f.slot == nil {
					f.slot = make([]int, slotNumbers)
				}
				for s := first; s <= last; s++ {
					f.slot[s] = prev
				}
				f.page[p] = pageMixed
			}
			for s := lo; s <= end; s++ {
				if o := f.slot[s]; o != noOwner && o != owner {
					taken(s, s, o)
				}
				f.slot[s] = owner
			}
			if whole {
				f.page[p] = owner
			}
		}
		lo = end + 1
	}
}

// slotWork is the working memory of the slot rules of one Check.
type slotWork struct {
	physical, allowed, list slotSet
	fixed                   fixedSlots
	// owners numbers the modules that fixed a slot of the device access
	// point being checked; names has their IDs.
	owners map[string]int
	names  []string
}

// checkSlots applies the slot rules to the module references of a device
// access point: a module allowed, used or fixed in a slot that is not
// among the PhysicalSlots, used or fixed where it is not allowed, and two
// modules fixed in one slot.
func (v *validator) checkSlots(dapPath func() string, dap *DeviceAccessPointItem, um *UseableModules) {
	if v.slots == nil {
		v.slots = &slotWork{owners: map[string]int{}}
		v.slots.physical.reset()
		v.slots.allowed.reset()
		v.slots.list.reset()
	}
	w := v.slots
	w.fixed.reset()
	clear(w.owners)
	w.names = w.names[:0]
	havePhysical := w.physical.parse(dap.PhysicalSlots)
	if havePhysical && w.physical.beyond > 0 {
		v.warn(dapPath, RuleSlots, "PhysicalSlots: %d values are above %d, the highest slot number", w.physical.beyond, slotNumbers-1)
	}
	var buf [maxSlotWarnings]uint32
	var prevs [maxSlotWarnings]int
	for _, ref := range um.ModuleItemRefs {
		rp := func() string {
			return dapPath() + "/UseableModules/ModuleItemRef[" + pathID(ref.ModuleItemTarget) + "]"
		}
		haveAllowed := w.allowed.parse(ref.AllowedInSlots)
		for i, text := range [...]string{ref.AllowedInSlots, ref.UsedInSlots, ref.FixedInSlots} {
			name, set := slotLists[i].name, &w.allowed
			if i == 0 {
				if !haveAllowed {
					continue
				}
			} else if set = &w.list; !set.parse(text) {
				continue
			}
			if set.beyond > 0 {
				v.warn(rp, RuleSlots, "%s: %d values are above %d, the highest slot number", name, set.beyond, slotNumbers-1)
			}
			// One warning per slot up to maxSlotWarnings, then one for
			// the rest.
			if havePhysical {
				first, total := set.missing(&w.physical, buf[:0])
				slotWarnings(v, rp, first, total, func(_ int, s uint32) string {
					return fmt.Sprintf("%s: slot %d is not among the PhysicalSlots %q", name, s, clip(dap.PhysicalSlots))
				}, slotLists[i].morePhysical)
			}
			if i > 0 && haveAllowed {
				first, total := set.missing(&w.allowed, buf[:0])
				slotWarnings(v, rp, first, total, func(_ int, s uint32) string {
					return fmt.Sprintf("%s: slot %d is not in AllowedInSlots %q", name, s, clip(ref.AllowedInSlots))
				}, slotLists[i].moreAllowed)
			}
			if i != 2 {
				continue
			}
			owner, known := w.owners[ref.ModuleItemTarget]
			if !known {
				owner = len(w.names)
				w.owners[ref.ModuleItemTarget] = owner
				w.names = append(w.names, ref.ModuleItemTarget)
			}
			first, total := buf[:0], 0
			set.ranges(func(lo, hi uint32) {
				w.fixed.fix(lo, hi, owner, func(a, b uint32, prev int) {
					total += int(b - a + 1)
					for s := a; s <= b && len(first) < maxSlotWarnings; s++ {
						prevs[len(first)] = prev
						first = append(first, s)
					}
				})
			})
			slotWarnings(v, rp, first, total, func(k int, s uint32) string {
				return fmt.Sprintf("slot %d is also fixed by module %s", s, clip(w.names[prevs[k]]))
			}, "%d more slots are also fixed by another module")
		}
	}
}
