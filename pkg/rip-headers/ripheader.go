package ripheaders

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"net/netip"
)

var (
	ErrInvalidCommand = errors.New("invalid command")
	errEntriesTooLong = errors.New("num_entries too long must not exceed 64")
	errEntriesRequest = errors.New("num_entries for request must be 0")
	errNilHeader      = errors.New("nil header")
	errHeaderTooShort = errors.New("header too short")
)

type HeaderCommand uint16

const (
	commandEntrySize = 4  // Size in bytes of command + num_entries
	hostLen          = 12 // Size of host struct
	INFINITY         = 16 // Max cost for route
	maxEntrySize     = 64
)

const (
	Request  HeaderCommand = 1
	Response HeaderCommand = 2
)

type RipHeader struct {
	Command     uint16
	Num_entries uint16
	Hosts       []Route
}

type Route struct {
	Cost    uint32
	Address uint32
	Mask    uint32
}

func (h *RipHeader) Marshal() ([]byte, error) {
	if h == nil {
		return nil, errNilHeader
	}
	if h.Command != uint16(Request) && h.Command != uint16(Response) {
		return nil, ErrInvalidCommand
	}
	if h.Command == uint16(Request) && h.Num_entries != 0 {
		return nil, errEntriesRequest
	}
	if h.Num_entries > maxEntrySize {
		return nil, errEntriesTooLong
	}
	b1 := make([]byte, 0)
	b1 = binary.BigEndian.AppendUint16(b1, h.Command)
	b1 = binary.BigEndian.AppendUint16(b1, h.Num_entries)

	// binary.BigEndian.PutUint16(b1[:2], h.Command)
	// binary.BigEndian.PutUint16(b1[2:4], h.Num_entries)

	for i := 0; i < len(h.Hosts); i++ {
		// tmp := make([]byte, hostLen)
		host := h.Hosts[i]
		b1 = binary.BigEndian.AppendUint32(b1, host.Cost)
		b1 = binary.BigEndian.AppendUint32(b1, host.Address)
		b1 = binary.BigEndian.AppendUint32(b1, host.Mask)

		// binary.BigEndian.PutUint32(tmp[:4], host.Cost)
		// binary.BigEndian.PutUint32(tmp[4:8], host.Address)
		// mask := ^uint32(0) << (32 - host.Mask)
		// binary.BigEndian.PutUint32(tmp[8:12], host.Mask)
		// b1 = append(b1, tmp...)
	}
	return b1, nil
}

func (h *RipHeader) Parse(b []byte) error {
	if h == nil || b == nil {
		return errNilHeader
	}
	h.Command = binary.BigEndian.Uint16(b[:2])
	h.Num_entries = binary.BigEndian.Uint16(b[2:4])
	if len(b) < commandEntrySize {
		return errHeaderTooShort
	}
	if h.Command != uint16(Request) && h.Command != uint16(Response) {
		return ErrInvalidCommand
	}
	if h.Command == uint16(Request) && h.Num_entries != 0 {
		return errEntriesRequest
	}

	zero_index := 4
	for i := 0; i < int(h.Num_entries); i++ {
		start := zero_index + 12*i
		cost := binary.BigEndian.Uint32(b[start : start+4])
		address := binary.BigEndian.Uint32(b[start+4 : start+8])
		mask := binary.BigEndian.Uint32(b[start+8 : start+12])
		host := Route{Cost: cost, Address: address, Mask: mask}
		h.Hosts = append(h.Hosts, host)

		// addrBuf := make([]byte, 4)
		// binary.BigEndian.PutUint32(addrBuf, host.Address)
		// addr, ok := netip.AddrFromSlice(addrBuf)
		// if !ok {
		// 	println("could not get ip in rip handler")
		// }
		// test := netip.PrefixFrom(addr, int(Mask2Bits(mask)))
		// fmt.Printf("Cost: %d, address: %s\n", cost, test)
	}

	if h.Num_entries > maxEntrySize || len(h.Hosts) > maxEntrySize {
		return errEntriesTooLong
	}
	return nil
}

func ParseHeader(b []byte) (*RipHeader, error) {
	h := new(RipHeader)
	if err := h.Parse(b); err != nil {
		return nil, err
	}
	return h, nil
}

func Mask2Bits(mask uint32) uint32 {
	// Bitwise and with mask
	return uint32(bits.OnesCount32(mask))
}

func Bits2Mask(bits uint32) uint32 {
	return ^uint32(0) << (32 - bits)
}

func (h *RipHeader) PrintHeader() {
	println()
	fmt.Printf("COMMAND: %d NUM ENTRIES: %d\n", h.Command, h.Num_entries)
	for _, host := range h.Hosts {
		addrBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(addrBuf, host.Address)
		addr, ok := netip.AddrFromSlice(addrBuf)
		if !ok {
			println("could not get ip in rip handler")
		}
		test := netip.PrefixFrom(addr, int(Mask2Bits(host.Mask)))
		fmt.Printf("COST: %d, ADDR: %s\n", host.Cost, test)
	}
}
