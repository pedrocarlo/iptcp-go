package protocol

import (
	"encoding/binary"
	ripheaders "iptcp-pedrocarlo/pkg/rip-headers"
	"net/netip"
	"time"
)

// TODO MAKE IT UNIFORM TO TRIGGER UPDATE
func RipHandler(d *Device, packet *Packet, _ []interface{}) {
	ripHeader, err := ripheaders.ParseHeader(packet.Data)
	if err != nil {
		return
	}
	d.addNeighboursIfStale(packet)

	triggered := make(RoutingTable)
	if ripHeader.Command == RipResponse {
		for _, host := range ripHeader.Hosts {
			addrBuf := make([]byte, 4)
			binary.BigEndian.PutUint32(addrBuf, host.Address)
			addr, ok := netip.AddrFromSlice(addrBuf)
			if !ok {
				println("could not get ip in rip handler")
				return
			}
			if !addr.IsValid() {
				println("ip not valid")
				return
			}
			// translate back the correct bit amount
			bitsMask := ripheaders.Mask2Bits(host.Mask)
			prefix := netip.PrefixFrom(addr, int(bitsMask))

			currHop, ok := d.Table[prefix]
			cost := host.Cost
			if cost > ripheaders.INFINITY {
				cost = ripheaders.INFINITY
			} else if cost < ripheaders.INFINITY {
				cost = cost + 1
			}
			// TODO see how to tell a far away router a router has disconnected
			if !ok {
				// Does not exist in table add to table
				if cost < ripheaders.INFINITY {
					d.Table[prefix] = Hop{Addr: packet.Header.Src, Cost: cost}
					triggered[prefix] = Hop{Addr: packet.Header.Src, Cost: cost}
				}
			} else {
				if cost < ripheaders.INFINITY {
					// If smaller cost or from same router at higher cost
					if cost < currHop.Cost {
						d.Table[prefix] = Hop{Addr: packet.Header.Src, Cost: cost}
						triggered[prefix] = Hop{Addr: packet.Header.Src, Cost: cost}
					} else if cost > currHop.Cost && currHop.Addr == packet.Header.Src {
						// Cost got higher from that particular hop
						d.Table[prefix] = Hop{Addr: packet.Header.Src, Cost: cost}
						triggered[prefix] = Hop{Addr: packet.Header.Src, Cost: cost}
					}
				} else {

					// Purge routes with more than = INFINITY that are not local to me and come from who I learned from
					if currHop.Cost > 0 && currHop.Addr == packet.Header.Src {
						delete(d.Table, prefix)
						triggered[prefix] = Hop{Addr: packet.Header.Src, Cost: cost}
					}
				}
			}
		}
		for _, router := range d.RipNeighbors {
			err := d.SendRip(RipResponse, router, triggered)
			if err != nil {
				continue
			}
		}

	} else if ripHeader.Command == ripRequest {
		// Send info to requesting router
		d.SendRip(RipResponse, packet.Header.Src, d.Table)
	}
}

func (d *Device) CreateRipPacket(command uint16, dst netip.Addr, table RoutingTable) (*ripheaders.RipHeader, error) {
	h := new(ripheaders.RipHeader)
	h.Command = command
	if ripheaders.Response == ripheaders.HeaderCommand(command) {
		for prefix, hop := range table {
			// Assuming here routers do not have 0.0.0.0 default addr
			prefixArray := prefix.Addr().As4()
			prefixBytes := prefixArray[0:]
			address := binary.BigEndian.Uint32(prefixBytes)
			cost := hop.Cost
			// Split Horizon with Poisoned Reverse
			if hop.Addr == dst {
				cost = ripheaders.INFINITY
			}

			mask := ripheaders.Bits2Mask(uint32(prefix.Bits()))
			h.Hosts = append(h.Hosts, ripheaders.Route{Cost: cost, Address: address, Mask: mask})
			h.Num_entries = uint16(len(h.Hosts))
		}
	} else if ripheaders.Request != ripheaders.HeaderCommand(command) {
		return nil, ripheaders.ErrInvalidCommand
	}
	return h, nil
}

func (d *Device) Rip() {
	for {
		d.Mutex.Lock()
		for _, router := range d.RipNeighbors {
			err := d.SendRip(RipResponse, router, d.Table)
			if err != nil {
				continue
			}
		}
		d.Mutex.Unlock()
		timer := time.NewTimer(5 * time.Second)
		<-timer.C
	}
}

func (d *Device) SendRip(command uint16, router netip.Addr, table RoutingTable) error {
	if len(table) == 0 {
		return nil
	}
	h, err := d.CreateRipPacket(command, router, table)
	if err != nil {
		// Put logger error here
		return err
	}
	ripBytes, err := h.Marshal()
	if err != nil {
		return err

	}
	_, err = d.SendIP(router, ripProtocol, ripBytes)
	if err != nil {
		// Put logger error here
		return err
	}
	return nil
}
