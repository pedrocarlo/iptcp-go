package ripheaders

import (
	"encoding/binary"
	"errors"
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
	b1 := make([]byte, commandEntrySize)
	binary.BigEndian.PutUint16(b1[:2], h.Command)
	binary.BigEndian.PutUint16(b1[2:4], h.Num_entries)

	for i := 0; i < int(h.Num_entries); i++ {
		tmp := make([]byte, hostLen)
		host := h.Hosts[i]
		binary.BigEndian.PutUint32(tmp[:4], host.Cost)
		binary.BigEndian.PutUint32(tmp[4:8], host.Address)
		binary.BigEndian.PutUint32(tmp[8:12], host.Mask)
		b1 = append(b1, tmp...)
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

	for i := 0; i < int(h.Num_entries); i++ {
		start := 4 * i
		cost := binary.BigEndian.Uint32(b[start : start+4])
		address := binary.BigEndian.Uint32(b[start+8 : start+12])
		mask := binary.BigEndian.Uint32(b[start+12 : start+16])
		host := Route{Cost: cost, Address: address, Mask: mask}
		h.Hosts = append(h.Hosts, host)
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
