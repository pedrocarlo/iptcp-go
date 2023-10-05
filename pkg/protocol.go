package pkg

import (
	"encoding/binary"
	lnxconfig "lnxconfig"
	"net"
	"net/netip"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
)

// type HandlerFunc = func(*Packet, []interface{})
// RegisterRecvHandler(protocolNum uint8, callbackFunc HandlerFunc)

type Packet struct {
	Header  ipv4header.IPv4Header
	DataLen int
	Data    []byte
}

type RoutingTable map[netip.Prefix]netip.AddrPort

type HandlerFunc = func(*Packet, []interface{})

type Device struct {
	Table       RoutingTable
	Neighbours  []Neighbour
	Interfaces  map[string]RouteInterface
	IsRouter    bool
	RoutingMode lnxconfig.RoutingMode
	Handlers    []HandlerFunc
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

// type Network struct {
// 	Interfaces map[string]lnxconfig.InterfaceConfig
// 	Mode       lnxconfig.RoutingMode
// 	Devices    []Device
// }

func Initialize(configInfo lnxconfig.IPConfig) (Device, error) {
	interfaces := configInfo.Interfaces
	neighbours := configInfo.Neighbors

	interMap := make(map[string]RouteInterface)
	for _, interf := range interfaces {
		routerInter := RouteInterface{Name: interf.Name, Ip: interf.AssignedIP, Prefix: interf.AssignedPrefix, UdpPort: interf.UDPAddr}
		interMap[interf.Name] = routerInter
	}

	neighbourSlice := make([]Neighbour, 1)
	for _, nei := range neighbours {
		neighbourSlice = append(neighbourSlice, Neighbour{InterfaceName: nei.InterfaceName, Ip: nei.DestAddr, UdpPort: nei.UDPAddr})
	}

	device := Device{Table: make(RoutingTable), Interfaces: interMap, Neighbours: neighbourSlice, IsRouter: false, RoutingMode: configInfo.RoutingMode}
	// TODO add handlers here with device created

	return device, nil
}

func Listen(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			// DROP PACKETS
			conn.Close()
			continue
		}
		// Read packet
	}
}

func Read(conn net.Conn) error {
	buf := make([]byte, 4)
	_, err := conn.Read(buf)
	if err != nil {
		return err
	}
	protocolV := int(binary.BigEndian.Uint32(buf))
	println(protocolV)
	return nil
}

// Handler for Configuring function to execute
func (d *Device) RegisterRecvHandler(protocolNum uint8, callbackFunc HandlerFunc) {
}

func (d *Device) SendIP(dst netip.Addr, protocolNum uint8, data []byte) error {
	return nil
}
