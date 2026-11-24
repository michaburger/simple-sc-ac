// Package version decides which interface version a session uses.
//
// The connecting party requests a version in its connect message. The accepting party
// answers with the version it supports or refuses with EPROTONOSUPPORT, and the connecting
// party may in turn refuse the answer. Major and minor version have to match, the patch
// version is ignored when versions are compared.
package version

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Version is major.minor.patch.
type Version struct{ Major, Minor, Patch int }

// Parse reads "major.minor.patch".
func Parse(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version: %q is not major.minor.patch", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return Version{}, fmt.Errorf("version: %q is not major.minor.patch", s)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

// MustParse is Parse for constants.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compatible reports whether major and minor are equal.
func (v Version) Compatible(o Version) bool { return v.Major == o.Major && v.Minor == o.Minor }

// Less orders by major, minor, patch.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// Sorted returns the versions sorted from lowest to highest.
func Sorted(vs []Version) []Version {
	out := append([]Version(nil), vs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

// Policy of the accepting party.
type Policy string

const (
	// Follow uses the requested version when it is supported (default).
	Follow Policy = "follow"
	// OfferHighest answers with the highest supported version of the same major version
	// first, also to a lower request, and falls back to Follow for a peer that refused it
	// before. A test option.
	OfferHighest Policy = "offer-highest"
)

// ParsePolicy accepts "follow" and "offer-highest".
func ParsePolicy(s string) (Policy, error) {
	switch p := Policy(s); p {
	case Follow, OfferHighest:
		return p, nil
	}
	return "", fmt.Errorf("version: unknown policy %q, want follow or offer-highest", s)
}

// ErrNotSupported means no common version exists. The connect is answered with
// EPROTONOSUPPORT.
type ErrNotSupported struct{ Requested Version }

func (e ErrNotSupported) Error() string {
	return fmt.Sprintf("version: %s not supported", e.Requested)
}

// Memory remembers peers that refused an offered version, per peer key (an EUI).
type Memory struct {
	mu      sync.Mutex
	refused map[string]Version
}

// NewMemory returns an empty memory.
func NewMemory() *Memory { return &Memory{refused: map[string]Version{}} }

// Refused records that peer refused v (or dropped the connection after it was offered).
func (m *Memory) Refused(peer string, v Version) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refused[peer] = v
}

// HasRefused reports whether peer refused a version compatible with v.
func (m *Memory) HasRefused(peer string, v Version) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.refused[peer]
	return ok && r.Compatible(v)
}

// Forget clears the memory for a peer.
func (m *Memory) Forget(peer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.refused, peer)
}

// Accept decides the answer of the accepting party (the SC) to a requested version.
// supported lists the versions for which a schema is loaded.
func Accept(policy Policy, requested Version, supported []Version, peer string, mem *Memory) (Version, error) {
	sup := Sorted(supported)
	var sameMajor []Version
	for _, v := range sup {
		if v.Major == requested.Major {
			sameMajor = append(sameMajor, v)
		}
	}
	if len(sameMajor) == 0 {
		return Version{}, ErrNotSupported{requested}
	}
	highest := sameMajor[len(sameMajor)-1]
	if policy == OfferHighest && !mem.HasRefused(peer, highest) {
		return highest, nil
	}
	for i := len(sameMajor) - 1; i >= 0; i-- {
		if sameMajor[i].Compatible(requested) {
			return sameMajor[i], nil
		}
	}
	if highest.Less(requested) {
		// Newer minor than we know: answer with our highest and let the peer decide.
		return highest, nil
	}
	return Version{}, ErrNotSupported{requested}
}

// Opener walks through the versions of the connecting party (the AC towards an SC): it
// requests the highest first and steps down after a refusal.
type Opener struct {
	supported []Version // highest first
	next      int
}

// NewOpener starts with the highest supported version. If mem says that peer refused it
// before, it starts below it.
func NewOpener(supported []Version, peer string, mem *Memory) *Opener {
	s := Sorted(supported)
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	o := &Opener{supported: s}
	for o.next < len(s)-1 && mem.HasRefused(peer, s[o.next]) {
		o.next++
	}
	return o
}

// Request returns the version to request, false when none is left.
func (o *Opener) Request() (Version, bool) {
	if o.next >= len(o.supported) {
		return Version{}, false
	}
	return o.supported[o.next], true
}

// Refused is called after the peer answered EPROTONOSUPPORT: the next connect requests the
// next lower version.
func (o *Opener) Refused() { o.next++ }

// Answer checks the version in the connect response. It returns the session version, or
// ErrNotSupported if this side has to refuse it with EPROTONOSUPPORT.
func (o *Opener) Answer(answered Version) (Version, error) {
	for _, v := range o.supported {
		if v.Compatible(answered) {
			return v, nil
		}
	}
	return Version{}, ErrNotSupported{answered}
}
