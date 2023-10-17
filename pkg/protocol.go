package protocol

import (
	"encoding/binary"
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
	ripResponse uint16 = 2
)

// type HandlerFunc = func(*Packet, []interface{})
// RegisterRecvHandler(protocolNum uint8, callbackFunc HandlerFunc)

type Packet struct {
	Header ipv4header.IPv4Header
	Data   []byte
}

type Hop struct {
	Addr        netip.Addr
	Cost        uint32
	LearnedFrom netip.Addr
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
		device.Table[interf.AssignedPrefix.Masked()] = Hop{Addr: interf.AssignedIP, Cost: 0, LearnedFrom: interf.AssignedPrefix.Addr()}
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
	// device := Device{Table: make(RoutingTable), Interfaces: interMap, Neighbours: neighbourSlice, IsRouter: isRouter, RoutingMode: configInfo.RoutingMode, RipNeighbors: configInfo.RipNeighbors, Handlers: make(map[uint8]func(*Packet, []interface{}))}

	for pre, route := range configInfo.StaticRoutes {
		device.Table[pre] = Hop{Addr: route, Cost: 0, LearnedFrom: route}
	}

	device.RegisterRecvHandler(testProtocol, TestHandler)
	if device.IsRouter {
		device.RegisterRecvHandler(ripProtocol, RipHandler)
	}

	listeners := make(map[string]*net.UDPConn, 0)
	listenChannels := make(map[string](chan []byte), 0)
	for _, inter := range device.Interfaces {
		// addr := fmt.Sprintf("%s:%d", inter.UdpPort.String(), inter.UdpPort.Port())
		addr := net.UDPAddr{
			Port: int(inter.UdpPort.Port()),
			IP:   inter.UdpPort.Addr().AsSlice(),
		}
		ln, err := net.ListenUDP("udp4", &addr)
		if err != nil {
			return nil, err
		}
		channel := make(chan []byte)
		listenChannels[inter.Name] = channel
		listeners[inter.Name] = ln
		go device.Listen(ln, channel)
	}
	device.Listeners = listeners

	// ripRequest to routers

	if isRouter {
		device.Mutex.Lock()
		ripNei := make([]netip.Addr, len(device.RipNeighbors))
		copy(ripNei, device.RipNeighbors)
		device.Mutex.Unlock()
		for _, router := range ripNei {
			err := device.sendRip(ripRequest, router)
			if err != nil {
				continue
			}
		}
		go device.Rip()
	}

	return device, nil
}

// Probably communicate via channels
func (d *Device) Listen(conn net.Conn, receivedChan chan []byte) error {
	size := MaxMessageSize
	for {
		// TODO ask about timeouts in GOlang professor
		buf := make([]byte, size)
		_, err := conn.Read(buf)
		if err != nil {
			// Drop Packets
			continue
		}
		// See timeout break here
		header, err := ipv4header.ParseHeader(buf)
		if err != nil {
			// Drop packet
			continue
		}

		data := buf[header.Len:header.TotalLen]

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

// TODO see if different interfaces interfere in this search for ip
func (d *Device) findIp(dst netip.Addr) (*netip.AddrPort, string, error) {
	prefix, ok := d.getDstPrefix(dst)

	if !ok {
		return nil, "", fmt.Errorf("could not find a neighbour to send")
	}

	var udpAddr netip.AddrPort
	var iface string
	d.Mutex.Lock()
	hop := d.Table[prefix]
	d.Mutex.Unlock()

	var next netip.Addr
	if prefix.Bits() == 0 || hop.Cost > 0 {
		next = hop.Addr
	} else {
		next = dst
	}
	// fmt.Printf("%s\n", next)

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
	d.Mutex.Lock()
	for pre := range d.Table {
		// if pre.Contains(dst) {
		// 	fmt.Printf("%s\n", pre)
		// }
		if pre.Contains(dst) && prefixSize <= pre.Bits() {
			prefix = pre.Masked()
			prefixSize = pre.Bits()
			found = true
		}
	}
	d.Mutex.Unlock()
	return prefix, found
}

func (d *Device) Handler(packet Packet) {
	if !d.isMyIp(packet.Header.Dst) {
		d.sendPacket(&packet)
		return
	}

	protocolNum := packet.Header.Protocol
	handler, ok := d.Handlers[uint8(protocolNum)]
	if ok {
		handler(d, &packet, nil)
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

// TODO Implement RIP
// Why do we need request?
// To know the router is online?
func RipHandler(d *Device, packet *Packet, _ []interface{}) {
	ripHeader, err := ripheaders.ParseHeader(packet.Data)
	if err != nil {
		return
	}
	if ripHeader.Command == ripResponse {
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

			d.Mutex.Lock()
			currHop, ok := d.Table[prefix]
			d.Mutex.Unlock()
			cost := host.Cost
			if !ok {
				if cost > ripheaders.INFINITY {
					cost = ripheaders.INFINITY
				} else if cost < ripheaders.INFINITY {
					cost = cost + 1
				}
				println(cost)
				// Does not exist in table add to table
				d.Mutex.Lock()
				d.Table[prefix] = Hop{Addr: addr, Cost: cost, LearnedFrom: packet.Header.Src}
				d.Mutex.Unlock()
			} else {
				// Maybe this is enough
				if cost < currHop.Cost {
					d.Mutex.Lock()
					d.Table[prefix] = Hop{Addr: addr, Cost: cost, LearnedFrom: packet.Header.Src}
					d.Mutex.Unlock()
				}
			}
		}
	} else if ripHeader.Command == ripRequest {
		// TODO Add to ripneighbour list if not there already??? Maybe
		found := false
		for _, rip := range d.RipNeighbors {
			if rip == packet.Header.Src {
				found = true
				break
			}
		}
		if !found {
			d.Mutex.Lock()
			d.RipNeighbors = append(d.RipNeighbors, packet.Header.Src)
			d.Mutex.Unlock()
		}
	}
}

// CHECK WHY THERE ARE MANY ENTRIES IN THE TABLE
func (d *Device) CreateRipPacket(command uint16, dst netip.Addr) (*ripheaders.RipHeader, error) {
	h := new(ripheaders.RipHeader)
	h.Command = command
	if ripheaders.Response == ripheaders.HeaderCommand(command) {
		d.Mutex.Lock()
		for prefix, hop := range d.Table {
			// Assuming here routers do not have 0.0.0.0 default addr
			prefixArray := prefix.Addr().As4()
			prefixBytes := prefixArray[0:]
			address := binary.BigEndian.Uint32(prefixBytes)
			cost := hop.Cost
			mask := ripheaders.Bits2Mask(uint32(prefix.Bits()))
			h.Hosts = append(h.Hosts, ripheaders.Route{Cost: cost, Address: address, Mask: mask})
			// Split Horizon with Poisoned Reverse
			if hop.LearnedFrom == dst {
				cost = ripheaders.INFINITY
			}
			// test := netip.PrefixFrom(netip.AddrFrom4(prefixArray), prefix.Bits())
			// fmt.Printf("COST: %d, ADDR: %s\n", cost, test)
			h.Num_entries = uint16(len(h.Hosts))
		}
		d.Mutex.Unlock()
	} else if ripheaders.Request != ripheaders.HeaderCommand(command) {
		return nil, ripheaders.ErrInvalidCommand
	}
	return h, nil
}

// TODO implement triggered updates somewhere
func (d *Device) Rip() {
	// Initial Setup to request to peers
	// Todo check when should request be made. Just on initialize?
	// Response is maybe done earlier in initialize
	for {
		// ripNei := make([]netip.Addr, len(d.RipNeighbors))
		// copy(ripNei, d.RipNeighbors)
		for _, router := range d.RipNeighbors {
			err := d.sendRip(ripResponse, router)
			if err != nil {
				continue
			}
		}
		timer := time.NewTimer(5 * time.Second)
		<-timer.C
	}

}

func (d *Device) sendRip(command uint16, router netip.Addr) error {
	h, err := d.CreateRipPacket(command, router)
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
	d.Mutex.Lock()
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
	d.Mutex.Unlock()
	w.Flush()
}
