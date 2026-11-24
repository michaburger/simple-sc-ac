// Package identity decides the own EUI-64 of simple-sc or simple-ac.
//
// Order: a configured value; otherwise a value stored at an earlier start; otherwise a
// locally administered EUI-64 derived from hardware and stored. The derived value starts
// with octet 0x02 (universal/local bit set, individual, the administratively assigned
// quadrant of IEEE 802c), the other 7 octets are the first 7 bytes of SHA-256 over the
// role, a hardware identifier and the data folder. It is stable on the same host, differs between SC and
// AC, and cannot collide with an EUI-64 assigned by the IEEE. The classic EUI-48 to EUI-64
// mapping (inserting FF-FE) is not used, the IEEE Registration Authority deprecates it.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/michaburger/simple-sc-ac/internal/common/idt"
)

// Source says where the EUI came from.
type Source string

const (
	Configured Source = "configured"
	Stored     Source = "stored"
	FromMAC    Source = "derived from a hardware MAC address"
	FromBalena Source = "derived from the balena device UUID"
	FromRandom Source = "random, no hardware identifier found"
)

// Result of Resolve.
type Result struct {
	EUI     idt.EUI64
	Source  Source
	Warning string
}

// Hardware returns the identifiers that can be used for the derivation. It is a variable
// so that tests can replace it.
var Hardware = func() (macs []net.HardwareAddr, balenaUUID string) {
	ifs, _ := net.Interfaces()
	sort.Slice(ifs, func(i, j int) bool { return ifs[i].Name < ifs[j].Name })
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) != 6 {
			continue
		}
		macs = append(macs, ifc.HardwareAddr)
	}
	return macs, os.Getenv("BALENA_DEVICE_UUID")
}

// Resolve returns the EUI for role ("sc" or "ac"). configured is the configuration value
// (may be empty), dataDir the state directory where a derived value is stored.
func Resolve(role, configured, dataDir string) (Result, error) {
	if role != "sc" && role != "ac" {
		return Result{}, fmt.Errorf("identity: unknown role %q", role)
	}
	if configured != "" {
		e, err := idt.ParseEUI64(configured)
		if err != nil {
			return Result{}, err
		}
		r := Result{EUI: e, Source: Configured}
		if e == 0 {
			r.Warning = "the EUI is all zeros: use this only for tests with a single instance, two programs with the same EUI cannot both connect to one Service Center"
		}
		return r, nil
	}
	path := filepath.Join(dataDir, "identity", role+".eui")
	if b, err := os.ReadFile(path); err == nil {
		e, err := idt.ParseEUI64(strings.TrimSpace(string(b)))
		if err != nil {
			return Result{}, fmt.Errorf("identity: %s: %w", path, err)
		}
		return Result{EUI: e, Source: Stored}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	abs, _ := filepath.Abs(dataDir)
	e, src, err := derive(role, abs)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, []byte(e.String()+"\n"), 0o644); err != nil {
		return Result{}, err
	}
	return Result{EUI: e, Source: src}, nil
}

// derive uses the hardware identifier, the role and the data folder, so that two
// installations on the same computer get different EUIs.
func derive(role, dataDir string) (idt.EUI64, Source, error) {
	macs, balena := Hardware()
	var input string
	var src Source
	// A universally administered MAC is a real hardware address. Containers usually see
	// a virtual, locally administered one, which would change with every container.
	for _, m := range macs {
		if m[0]&0x02 == 0 {
			input, src = m.String(), FromMAC
			break
		}
	}
	if input == "" && balena != "" {
		input, src = "balena:"+balena, FromBalena
	}
	if input == "" && len(macs) > 0 {
		input, src = macs[0].String(), FromMAC
	}
	if input == "" {
		var r [16]byte
		if _, err := rand.Read(r[:]); err != nil {
			return 0, "", err
		}
		input, src = fmt.Sprintf("random:%x", r), FromRandom
	}
	return fromInput(role, input+"|"+dataDir), src, nil
}

func fromInput(role, input string) idt.EUI64 {
	h := sha256.Sum256([]byte("simple-sc-ac|" + role + "|" + input))
	var b [8]byte
	b[0] = 0x02
	copy(b[1:], h[:7])
	return idt.EUI64(binary.BigEndian.Uint64(b[:]))
}
