package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	ripheaders "iptcp-pedrocarlo/pkg/rip-headers"
	lnxconfig "lnxconfig"
	"log"
	"net"
	"net/netip"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
	"github.com/google/netstack/tcpip/header"
)

const (
	MaxMessageSize       = 1400
	testProtocol   uint8 = 0
	ripProtocol    uint8 = 200
)

const (
	ripRequest  uint16 = 1
	RipResponse uint16 = 2
)

var (
	errInterfaceDown = errors.New("interface is down")
)

type Packet struct {
	Header ipv4header.IPv4Header
	Data   []byte
}

type Hop struct {
	Addr netip.Addr
	Cost uint32
}

type RoutingTable map[netip.Prefix]Hop

type HandlerFunc = func(*Device, *Packet, []interface{})

type Device struct {
	Table        RoutingTable
	Neighbours   []Neighbour
	Interfaces   map[string]*RouteInterface
	IsRouter     bool
	RoutingMode  lnxconfig.RoutingMode
	RipNeighbors []netip.Addr
	ripChannels  map[netip.Addr]chan bool
	Handlers     map[uint8]HandlerFunc
	Listeners    map[string]*net.UDPConn // string interface names
	Mutex        *sync.Mutex
}

type RouteInterface struct {
	Name    string
	Ip      netip.Addr
	Prefix  netip.Prefix
	UdpPort netip.AddrPort
	IsUp    bool
}

type Neighbour struct {
	InterfaceName string
	Ip            netip.Addr
	UdpPort       netip.AddrPort
}

func Initialize(configInfo lnxconfig.IPConfig) (*Device, error) {
	device := new(Device)
	interfaces := configInfo.Interfaces
	neighbours := configInfo.Neighbors
	isRouter := configInfo.RoutingMode != lnxconfig.RoutingTypeNone
	interMap := make(map[string]*RouteInterface)

	device.Table = make(RoutingTable)
	device.Handlers = make(map[uint8]HandlerFunc)

	for _, interf := range interfaces {
		routerInter := RouteInterface{Name: interf.Name, Ip: interf.AssignedIP, Prefix: interf.AssignedPrefix, UdpPort: interf.UDPAddr, IsUp: true}
		interMap[interf.Name] = &routerInter
		// Populating table with interface local prefixes
		device.Table[interf.AssignedPrefix.Masked()] = Hop{Addr: interf.AssignedIP, Cost: 0}
	}

	neighbourSlice := make([]Neighbour, 0)
	for _, nei := range neighbours {
		neighbourSlice = append(neighbourSlice, Neighbour{InterfaceName: nei.InterfaceName, Ip: nei.DestAddr, UdpPort: nei.UDPAddr})
	}

	device.Mutex = &sync.Mutex{}
	device.Interfaces = interMap
	device.Neighbours = neighbourSlice
	device.IsRouter = isRouter
	device.RoutingMode = configInfo.RoutingMode
	device.RipNeighbors = configInfo.RipNeighbors

	for pre, route := range configInfo.StaticRoutes {
		device.Table[pre] = Hop{Addr: route, Cost: 0}
	}

	device.RegisterRecvHandler(testProtocol, TestHandler)
	if device.IsRouter {
		device.RegisterRecvHandler(ripProtocol, RipHandler)
	}

	listeners := make(map[string]*net.UDPConn, 0)
	// listenChannels := make(map[string](chan netip.Addr), 5)
	for _, inter := range device.Interfaces {
		addr := net.UDPAddr{
			Port: int(inter.UdpPort.Port()),
			IP:   inter.UdpPort.Addr().AsSlice(),
		}
		ln, err := net.ListenUDP("udp4", &addr)
		if err != nil {
			return nil, err
		}
		// channel := make(chan netip.Addr)
		// listenChannels[inter.Name] = channel
		listeners[inter.Name] = ln
		go device.Listen(ln, inter)
	}
	device.Listeners = listeners

	// ripRequest to routers
	device.ripChannels = make(map[netip.Addr]chan bool)
	if isRouter {
		ripNei := make([]netip.Addr, len(device.RipNeighbors))
		copy(ripNei, device.RipNeighbors)
		for _, router := range ripNei {
			err := device.SendRip(ripRequest, router, device.Table)
			if err != nil {
				continue
			}
			device.ripChannels[router] = make(chan bool, 5)
			channel := device.ripChannels[router]
			go device.timeout(channel, router)
		}
		go device.Rip()
	}

	return device, nil
}

// Probably communicate via channels
func (d *Device) Listen(conn net.Conn, inter *RouteInterface) error {
	size := MaxMessageSize
	for {
		if !inter.IsUp {
			// Do not listen if interface down
			continue
		}
		buf := make([]byte, size)
		_, err := conn.Read(buf)
		if err != nil {
			// Drop Packets
			continue
		}
		header, err := ipv4header.ParseHeader(buf)
		if err != nil {
			// Drop packet
			continue
		}

		data := buf[header.Len:header.TotalLen]
		go d.timeoutHandler(header.Src)
		go d.Handler(Packet{Header: *header, Data: data})
	}
}

// Handler for Configuring function to execute
func (d *Device) RegisterRecvHandler(protocolNum uint8, callbackFunc HandlerFunc) {
	d.Handlers[protocolNum] = callbackFunc
}

func (d *Device) createPacket(dst netip.Addr, protocolNum uint8, data []byte) *Packet {
	h := ipv4header.IPv4Header{}
	h.Version = ipv4header.Version
	h.Len = ipv4header.HeaderLen
	h.TOS = 0
	h.TotalLen = h.Len + len(data) // Believe this is correct
	h.ID = 0
	h.Flags = 0
	h.FragOff = 0
	h.TTL = 16
	h.Protocol = int(protocolNum)

	// Default interface Change later in sendPacket
	h.Src = d.Interfaces["if0"].Ip
	h.Dst = dst
	h.Options = make([]byte, 0)
	h.Checksum = 0
	headerBytes, err := h.Marshal()
	if err != nil {
		log.Fatalln("Error marshalling header:  ", err)
	}

	h.Checksum = int(ComputeChecksum(headerBytes))
	return &Packet{Header: h, Data: data}
}

func (d *Device) findIp(dst netip.Addr) (*netip.AddrPort, string, error) {
	prefix, ok := d.getDstPrefix(dst)

	if !ok {
		return nil, "", fmt.Errorf("could not find a neighbour to send")
	}

	var udpAddr netip.AddrPort
	var iface string
	hop := d.Table[prefix]

	var next netip.Addr
	if prefix.Bits() == 0 || hop.Cost > 0 {
		next = hop.Addr
	} else {
		next = dst
	}

	for _, neighbour := range d.Neighbours {
		if neighbour.Ip == next {
			udpAddr = neighbour.UdpPort
			iface = neighbour.InterfaceName
			break
		}
	}

	// If error probably drop packet
	if iface == "" {
		return nil, "", fmt.Errorf("could not find interface to send")
	}

	return &udpAddr, iface, nil
}

func (d *Device) sendPacket(p *Packet) (int, error) {

	udpAddr, iface, err := d.findIp(p.Header.Dst)
	if err != nil {
		return 0, err
	}

	if !d.Interfaces[iface].IsUp {
		return 0, errInterfaceDown
	}

	// Change src addr to be correct interface
	p.Header.Src = d.Interfaces[iface].Ip

	conn := d.Listeners[iface]
	if err != nil {
		return 0, err
	}
	slice, err := p.Header.Marshal()
	if err != nil {
		return 0, err
	}
	slice = append(slice, p.Data...)
	n, err := conn.WriteToUDP(slice, net.UDPAddrFromAddrPort(*udpAddr))
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (d *Device) SendIP(dst netip.Addr, protocolNum uint8, data []byte) (int, error) {
	packet := d.createPacket(dst, protocolNum, data)
	for _, iface := range d.Interfaces {
		// Destination is to one of its interfaces
		if iface.Ip == packet.Header.Dst {
			d.Handler(*packet)
			return 0, nil
		}
	}
	n, err := d.sendPacket(packet)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (d *Device) getDstPrefix(dst netip.Addr) (netip.Prefix, bool) {
	prefixSize := 0
	var prefix netip.Prefix
	found := false
	for pre := range d.Table {
		if pre.Contains(dst) && prefixSize <= pre.Bits() {
			prefix = pre.Masked()
			prefixSize = pre.Bits()
			found = true
		}
	}
	return prefix, found
}

func (d *Device) Handler(packet Packet) {

	b, err := packet.Header.Marshal()

	if err != nil {
		return
	}
	if !ValidateChecksum(b, uint16(packet.Header.Checksum)) {
		return
	}

	if !d.isMyIp(packet.Header.Dst) {
		d.sendPacket(&packet)
		return
	}

	d.timeoutHandler(packet.Header.Src)
	protocolNum := packet.Header.Protocol
	handler, ok := d.Handlers[uint8(protocolNum)]
	if ok {
		d.Mutex.Lock()
		handler(d, &packet, nil)
		d.Mutex.Unlock()
		// Drop packet is just not handling here
	}
}

func (d *Device) isMyIp(dst netip.Addr) bool {
	for _, inter := range d.Interfaces {
		if inter.Ip == dst {
			return true
		}
	}
	return false
}

func TestHandler(d *Device, packet *Packet, _ []interface{}) {
	fmt.Println()
	fmt.Printf("> Received test packet: Src: %s, Dst: %s, TTL: %d, Data: %s\n> ", packet.Header.Src, packet.Header.Dst, packet.Header.TTL, packet.Data)
}

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

func (d *Device) timeoutHandler(ip netip.Addr) {
	d.Mutex.Lock()
	channel, ok := d.ripChannels[ip]
	d.Mutex.Unlock()
	if ok {
		channel <- true
	}
}

// Buffered received Channel
func (d *Device) timeout(received chan bool, ip netip.Addr) {
	for {
		select {
		case <-received:
			continue
		case <-time.NewTimer(12 * time.Second).C:
			// Timeout RIP NEIGHBOUR
			var index int
			var found bool = false
			d.Mutex.Lock()
			for i, addr := range d.RipNeighbors {
				if addr == ip {
					index = i
					found = true
					break
				}
			}
			if found {
				// purge entries it learned from that router
				d.RipNeighbors = remove(d.RipNeighbors, index)
				close(d.ripChannels[ip])
				delete(d.ripChannels, ip)

				tmp := make(RoutingTable, 0)
				pre2Delete := make([]netip.Prefix, 0)
				for prefix, hop := range d.Table {
					if hop.Addr == ip {
						pre2Delete = append(pre2Delete, prefix)
						tmp[prefix] = Hop{hop.Addr, ripheaders.INFINITY}
					} else {
						tmp[prefix] = Hop{hop.Addr, hop.Cost}
					}
				}
				// Tell neighbours unreachable routes
				for _, pre := range pre2Delete {
					delete(d.Table, pre)
				}
				for _, nei := range d.RipNeighbors {
					d.SendRip(RipResponse, nei, tmp)
				}
			}
			d.Mutex.Unlock()
			return
		}
	}
}

func remove(s []netip.Addr, i int) []netip.Addr {
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}

func (d *Device) addNeighboursIfStale(packet *Packet) {
	// If not already in the list because it became stale add it
	found := false
	for _, rip := range d.RipNeighbors {
		if rip == packet.Header.Src {
			found = true
			break
		}
	}
	if !found {
		d.RipNeighbors = append(d.RipNeighbors, packet.Header.Src)
		d.ripChannels[packet.Header.Src] = make(chan bool, 5)
		go d.timeout(d.ripChannels[packet.Header.Src], packet.Header.Src)
	}
}

// Compute the checksum using the netstack package
// GOT FROM UDP IN IP EXAMPLE
func ComputeChecksum(b []byte) uint16 {
	checksum := header.Checksum(b, 0)
	checksumInv := checksum ^ 0xffff

	return checksumInv
}

func ValidateChecksum(b []byte, fromHeader uint16) bool {
	checksum := header.Checksum(b, fromHeader)

	return fromHeader == checksum
}

func ListRoutes(d *Device) {
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "T\tPrefix\tNext hop\tCost\t")
	for pre, hop := range d.Table {
		var t string
		if pre.Bits() == 0 {
			t = "S"
		} else if hop.Cost == 0 {
			t = "L"
		} else {
			t = "R"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t\n", t, pre, hop.Addr, hop.Cost)
	}
	w.Flush()
}
