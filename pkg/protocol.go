package pkg

import (
	"encoding/binary"
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
	Interfaces   map[string]RouteInterface
	IsRouter     bool
	RoutingMode  lnxconfig.RoutingMode
	RipNeighbors []netip.Addr
	Handlers     map[uint8]HandlerFunc
	Listeners    []*net.UDPConn
}

type RouteInterface struct {
	Name    string
	Ip      netip.Addr
	Prefix  netip.Prefix
	UdpPort netip.AddrPort
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
	interMap := make(map[string]RouteInterface)
	for _, interf := range interfaces {
		routerInter := RouteInterface{Name: interf.Name, Ip: interf.AssignedIP, Prefix: interf.AssignedPrefix, UdpPort: interf.UDPAddr}
		interMap[interf.Name] = routerInter
	}

	neighbourSlice := make([]Neighbour, 1)
	for _, nei := range neighbours {
		neighbourSlice = append(neighbourSlice, Neighbour{InterfaceName: nei.InterfaceName, Ip: nei.DestAddr, UdpPort: nei.UDPAddr})
	}

	device := Device{Table: make(RoutingTable), Interfaces: interMap, Neighbours: neighbourSlice, IsRouter: isRouter, RoutingMode: configInfo.RoutingMode, RipNeighbors: configInfo.RipNeighbors, Handlers: make(map[uint8]func(*Packet, []interface{}))}

	for pre, route := range lnxconfig.LnxConfig.StaticRoutes {
		device.Table[pre] = route
	}

	listeners := make([]*net.UDPConn, 2)
	listenChannels := make([]chan []byte, 2)
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
		listenChannels = append(listenChannels, channel)
		listeners = append(listeners, ln)
		go Listen(ln, channel)
	}
	device.Listeners = listeners
	device.RegisterRecvHandler(testProtocol, TestHandler)
	if device.IsRouter {
		device.RegisterRecvHandler(ripProtocol, RipHandler)
	}

	return &device, nil
}

// Probably communicate via channels
// TODO ask if it could happen for it to receive at the same time two different packages
func Listen(conn net.Conn, receivedChan chan []byte) error {
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
		// Recompute checksum

		// Read Data
		// For now assuming I receive just one big packet of udp
		header.TTL -= 1
		if !ValidateChecksum(header) {
			// Drop packet
			continue
		}
	}
}

// Handler for Configuring function to execute
func (d *Device) RegisterRecvHandler(protocolNum uint8, callbackFunc HandlerFunc) {
	d.Handlers[protocolNum] = callbackFunc
}

func (d *Device) createPacket(dst netip.Addr, protocolNum uint8, data []byte) *Packet {
	h := ipv4header.IPv4Header{}
	h.Version = 4
	h.TOS = 0                         // Dont know what to put here
	h.TotalLen = len(data)            // Maybe this field is for data
	h.ID = 0                          // TODO see what to put here
	h.Flags = ipv4header.DontFragment // TODO
	h.FragOff = 0                     // TODO
	h.TTL = 16
	h.Protocol = int(protocolNum)

	// Default interface
	h.Src = d.Interfaces["if0"].Ip
	h.Dst = dst
	h.Options = make([]byte, 0)
	h.Checksum = ComputeCheckSum(&h)
	return &Packet{Header: h, Data: data}
}

func (d *Device) sendPacket(p *Packet) error {
	prefix := d.getDstPrefix(p.Header.Dst)
	// Find neighbour addr in table in the table
	addr := d.Table[prefix]
	// Find neighbour to forward
	var udpAddr netip.AddrPort
	for _, neighbour := range d.Neighbours {
		if neighbour.Ip == addr {
			udpAddr = neighbour.UdpPort
			break
		}
	}
	conn, err := net.Dial("udp4", udpAddr.String())
	if err != nil {
		return err
	}
	headerByte, err := p.Header.Marshal()
	if err != nil {
		println(err)
		return err
	}
	_, err = conn.Write(headerByte)
	if err != nil {
		println(err)
		return err
	}
	_, err = conn.Write(p.Data)
	if err != nil {
		println(err)
		return err
	}
	return nil
}

func (d *Device) SendIP(dst netip.Addr, protocolNum uint8, data []byte) error {
	packet := d.createPacket(dst, protocolNum, data)
	err := d.sendPacket(packet)
	if err != nil {
		return err
	}
	return nil
}

func (d *Device) getDstPrefix(dst netip.Addr) netip.Prefix {
	prefixSize := 0
	var prefix netip.Prefix
	for pre, _ := range d.Table {
		if pre.Contains(dst) && prefixSize < pre.Bits() {
			prefix = pre
			prefixSize = pre.Bits()
		}
	}
	return prefix
}

func (d *Device) Handler(chan ipv4header.IPv4Header) {

}

func TestHandler(packet *Packet, _ []interface{}) {
	os.Stdout.Write(packet.Data)
}

// TODO Implement RIP
func RipHandler(packet *Packet, _ []interface{}) {
	os.Stdout.Write(packet.Data)
}

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
