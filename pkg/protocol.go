package protocol

import (
	"encoding/binary"
	"fmt"
	lnxconfig "lnxconfig"
	"net"
	"net/netip"
	"os"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
)

const (
	MaxMessageSize       = 1400
	testProtocol   uint8 = 0
	ripProtocol    uint8 = 200
)

// type HandlerFunc = func(*Packet, []interface{})
// RegisterRecvHandler(protocolNum uint8, callbackFunc HandlerFunc)

type Packet struct {
	Header ipv4header.IPv4Header
	Data   []byte
}

type RoutingTable map[netip.Prefix]netip.Addr

type HandlerFunc = func(*Packet, []interface{})

type Device struct {
	Table        RoutingTable
	Neighbours   []Neighbour
	Interfaces   map[string]*RouteInterface
	IsRouter     bool
	RoutingMode  lnxconfig.RoutingMode
	RipNeighbors []netip.Addr
	Handlers     map[uint8]HandlerFunc
	Listeners    map[string]*net.UDPConn // string interface names
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
	interfaces := configInfo.Interfaces
	neighbours := configInfo.Neighbors
	isRouter := configInfo.RoutingMode != lnxconfig.RoutingTypeNone
	interMap := make(map[string]*RouteInterface)
	for _, interf := range interfaces {
		routerInter := RouteInterface{Name: interf.Name, Ip: interf.AssignedIP, Prefix: interf.AssignedPrefix, UdpPort: interf.UDPAddr, IsUp: true}
		interMap[interf.Name] = &routerInter
	}

	neighbourSlice := make([]Neighbour, 0)
	for _, nei := range neighbours {
		neighbourSlice = append(neighbourSlice, Neighbour{InterfaceName: nei.InterfaceName, Ip: nei.DestAddr, UdpPort: nei.UDPAddr})
	}

	device := Device{Table: make(RoutingTable), Interfaces: interMap, Neighbours: neighbourSlice, IsRouter: isRouter, RoutingMode: configInfo.RoutingMode, RipNeighbors: configInfo.RipNeighbors, Handlers: make(map[uint8]func(*Packet, []interface{}))}

	for pre, route := range lnxconfig.LnxConfig.StaticRoutes {
		device.Table[pre] = route
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

	return &device, nil
}

// Probably communicate via channels
// TODO ask if it could happen for it to receive at the same time two different packages
func (d *Device) Listen(conn net.Conn, receivedChan chan []byte) error {
	// IP Header Size and options got this from lecture 7 photo
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
			// TODO see what to do with error here
			// Drop packet
			continue
		}

		data := buf[header.Len:header.TotalLen]

		go d.Handler(Packet{Header: *header, Data: data})

		// TODO see this in routing

		// Recompute checksum
		// For now assuming I receive just one big packet of udp
		// header.TTL -= 1
		// if !ValidateChecksum(header) {
		// 	// Drop packet
		// 	continue
		// }
	}
}

// Handler for Configuring function to execute
func (d *Device) RegisterRecvHandler(protocolNum uint8, callbackFunc HandlerFunc) {
	d.Handlers[protocolNum] = callbackFunc
}

func (d *Device) createPacket(dst netip.Addr, protocolNum uint8, data []byte) *Packet {
	h := ipv4header.IPv4Header{}
	h.Version = 4
	h.Len = 20                     // TODO just put it to no fail
	h.TOS = 0                      // Dont know what to put here
	h.TotalLen = h.Len + len(data) // Believe this is correct
	h.ID = 0                       // TODO see what to put here
	h.Flags = 0                    // TODO
	h.FragOff = 0                  // TODO
	h.TTL = 32                     // TODO should be 16 just setting to 32 to test
	h.Protocol = int(protocolNum)

	// Default interface
	h.Src = d.Interfaces["if0"].Ip
	h.Dst = dst
	h.Options = make([]byte, 0)
	h.Checksum = ComputeCheckSum(&h)
	return &Packet{Header: h, Data: data}
}

// TODO see if different interfaces interfere in this search for ip
func (d *Device) findIp(dst netip.Addr) (*netip.AddrPort, string, error) {
	prefix := d.getDstPrefix(dst)

	// Find neighbour to forward
	var udpAddr netip.AddrPort
	var iface string
	found := false
	for _, neighbour := range d.Neighbours {
		if neighbour.Ip == dst {
			udpAddr = neighbour.UdpPort
			iface = neighbour.InterfaceName
			found = true
			break
		}
	}
	if !found {
		addr, ok := d.Table[prefix]
		if !ok {
			// Find router to forward
			for _, nei := range d.Neighbours {
				if nei.Ip == addr {
					udpAddr = nei.UdpPort
					iface = nei.InterfaceName
				}
			}
		}
	}

	// If error probably drop packert
	if iface == "" {
		return nil, "", fmt.Errorf("could not find a neighbour to send")
	}

	return &udpAddr, iface, nil
}

// TODO have to see when the package is destined to youeself to not send via internet
func (d *Device) sendPacket(p *Packet) (int, error) {
	udpAddr, iface, err := d.findIp(p.Header.Dst)
	if err != nil {
		return 0, err
	}

	conn := d.Listeners[iface]

	// conn, err := net.Dial("udp4", udpAddr.String())
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

func (d *Device) getDstPrefix(dst netip.Addr) netip.Prefix {
	prefixSize := 0
	var prefix netip.Prefix
	for pre := range d.Table {
		if pre.Contains(dst) && prefixSize < pre.Bits() {
			prefix = pre
			prefixSize = pre.Bits()
		}
	}
	return prefix
}

func (d *Device) Handler(packet Packet) {
	protocolNum := packet.Header.Protocol
	handler, ok := d.Handlers[uint8(protocolNum)]
	if ok {
		handler(&packet, nil)
		// Drop packet is just not handling here
	}
}

func TestHandler(packet *Packet, _ []interface{}) {
	fmt.Println()
	fmt.Printf("> Received test packet: Src: %s, Dst: %s, TTL: %d, Data: %s\n> ", packet.Header.Src, packet.Header.Dst, packet.Header.TTL, packet.Data)
}

// TODO Implement RIP
func RipHandler(packet *Packet, _ []interface{}) {
	os.Stdout.Write(packet.Data)
}

// TODO see how to compute this
func ComputeCheckSum(h *ipv4header.IPv4Header) int {
	sum := 0
	sum += h.Version
	sum += h.Len
	sum += h.TOS
	sum += h.TotalLen
	sum += h.ID
	sum += int(h.Flags)
	sum += h.FragOff
	sum += h.TTL
	sum += h.Protocol
	sum += int(binary.BigEndian.Uint32(h.Src.AsSlice()))
	sum += int(binary.BigEndian.Uint32(h.Dst.AsSlice()))
	return sum
}

func ValidateChecksum(h *ipv4header.IPv4Header) bool {
	return ComputeCheckSum(h) == h.Checksum
}
