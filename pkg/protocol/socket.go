package protocol

import (
	"errors"
	"fmt"
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"
	"time"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
	"github.com/google/netstack/tcpip/header"
)

var (
	errPortInUse = errors.New("port already in use")
	errTimeout   = errors.New("connection timed out")
)

type Status uint8

// All Tcp Sockets states supposedly
const (
	Listen Status = iota
	Established
	SynSent
	SynRecv
	FinWait1
	FinWait2
	TimeWait
	Closed
	CloseWait
	LastAck
	Closing
)

type Transport uint16

const (
	test Transport = iota
	udp  Transport = 2
	tcp  Transport = 6
)

const (
	SYN   = header.TCPFlagSyn
	ACK   = header.TCPFlagAck
	RESET = header.TCPFlagRst
	FIN   = header.TCPFlagFin
	URG   = header.TCPFlagUrg
	PUSH  = header.TCPFlagPsh
)

type Socket interface {
	GetRemote() netip.AddrPort
	GetLocal() netip.AddrPort
	GetStatus() Status
}

type SocketKey struct {
	remote       netip.AddrPort
	host         netip.AddrPort
	trasportType Transport
}

type VTcpListener struct {
	remoteAddr    netip.AddrPort
	localAddr     netip.AddrPort
	d             *Device
	listenChannel chan VTcpConn // Send remote Addr
}

type VTcpConn struct {
	remoteAddr    netip.AddrPort
	localAddr     netip.AddrPort
	d             *Device
	listenChannel chan tcpheader.TcpPacket // TODO CHANGE LATER
	status        Status
	tcb           TCB
}

type ListenTable map[SocketKey]*VTcpListener
type ConnTable map[SocketKey]*VTcpConn

func tcpHandler(d *Device, p *Packet, _ []interface{}) {
	tcpHdr := tcpheader.ParseTCPHeader(p.Data)
	tcpPayload := p.Data[tcpHdr.DataOffset:]
	tcpChecksumFromHeader := tcpHdr.Checksum
	tcpHdr.Checksum = 0
	tcpComputedChecksum := tcpheader.ComputeTCPChecksum(&tcpHdr, p.Header.Src, p.Header.Dst, tcpPayload)
	if tcpChecksumFromHeader != tcpComputedChecksum {
		return
	}
	tcpPacket := tcpheader.TcpPacket{TcpHdr: tcpHdr, Payload: tcpPayload}
	// DO something with the payload
	go handlerFlags(d, p, &tcpPacket)
}

func handlerFlags(d *Device, p *Packet, tcpPacket *tcpheader.TcpPacket) {
	key := SocketKeyFromPacketAndTcpHdr(p.Header, tcpPacket.TcpHdr)
	tcpHdr := tcpPacket.TcpHdr
	switch tcpHdr.Flags {
	case SYN:
		ln, ok := d.listenTable[key]
		// Connect only if in table
		if ok {
			conn := d.CreateSocket(key.remote, key.host.Port())
			conn.tcb.initialAck = tcpHdr.SeqNum
			conn.tcb.currAck = tcpHdr.SeqNum
			ln.listenChannel <- *conn
		}
	case SYN + ACK:
		// Syn Received
		conn, ok := d.connTable[key]
		if ok {
			tcpPacket := conn.CreateTcpPacket(tcpHdr.SeqNum+1, tcpHdr.AckNum, ACK, make([]byte, 1))
			conn.listenChannel <- tcpPacket
			// Send Ack Back
			d.SendTcp(key.remote.Addr(), tcpPacket) // See if I care about return values here
		}
	}
}

func SocketKeyFromPacketAndTcpHdr(ipHdr ipv4header.IPv4Header, tcpHdr header.TCPFields) SocketKey {
	return SocketKey{
		remote:       netip.AddrPortFrom(ipHdr.Src, tcpHdr.SrcPort),
		host:         netip.AddrPortFrom(ipHdr.Dst, tcpHdr.DstPort),
		trasportType: tcp,
	}
}

/* Start Listen Socket Api */

// TODO will add tables to device in protocol but see if it is best approach
func (d *Device) VListen(port uint16) (*VTcpListener, error) {
	// TODO check if it is okay to use must parse here
	remoteAddrPort := netip.MustParseAddrPort("0.0.0.0:0")
	localAddrPort := netip.MustParseAddrPort(fmt.Sprintf("0.0.0.0:%d", port))
	key := SocketKey{remote: remoteAddrPort, host: localAddrPort, trasportType: tcp}
	// Check in table
	_, ok := d.listenTable[key]
	if ok {
		return nil, errPortInUse
	}
	// Spawn a thread
	ln := &VTcpListener{remoteAddr: remoteAddrPort, localAddr: localAddrPort, d: d, listenChannel: make(chan VTcpConn)}
	d.listenTable[key] = ln

	return ln, nil
}

// See later timeout here if does not receive second part of the handshake
func (ln *VTcpListener) VAccept() (*VTcpConn, error) {
	conn := <-ln.listenChannel
	// conn := ln.d.CreateSocket(key.remote, key.host.Port())
	key := SocketKey{remote: conn.remoteAddr, host: conn.localAddr, trasportType: tcp}
	ln.d.connTable[key] = &conn
	// Send Syn Ack
	// TODO need to have the initial seq number here to edit in tcb
	tcpPacket := conn.CreateTcpPacket(conn.tcb.initialSeq+1, conn.tcb.initialAck, SYN+ACK, make([]byte, 1))
	_, err := conn.d.SendTcp(conn.remoteAddr.Addr(), tcpPacket)
	if err != nil {
		return nil, err
	}
	// Wait for Ack from channel else timeout
	select {
	case responsePacket := <-conn.listenChannel:
		// TODO Make sure that it is an ACK
		conn.status = Established
		conn.tcb.initialAck = responsePacket.TcpHdr.SeqNum
		conn.tcb.currAck = responsePacket.TcpHdr.SeqNum
		return &conn, nil
	case <-time.NewTimer(time.Second).C:
		return nil, errTimeout
	}
}

func (ln *VTcpListener) VClose() error {
	return nil
}

func (ln *VTcpListener) GetRemote() netip.AddrPort {
	return ln.remoteAddr
}

func (ln *VTcpListener) GetLocal() netip.AddrPort {
	return ln.localAddr
}

func (ln *VTcpListener) GetStatus() Status {
	return Listen
}

func listen(ln *VTcpListener, remoteChan chan netip.AddrPort) {
	for {
		// remoteAddrPort := <-remoteChan
		// println(remoteAddrPort)
	}
}

/* End Listen Socket Api */

/* Start Normal Socket Api */

func (d *Device) CreateSocket(remoteAddrPort netip.AddrPort, localPort uint16) *VTcpConn {
	return &VTcpConn{remoteAddr: remoteAddrPort, localAddr: netip.AddrPortFrom(d.Interfaces["if0"].Ip, localPort), d: d, status: Closed, tcb: *createTCB()}
}

// For now do not choose a random port
func (d *Device) VConnect(addr netip.Addr, port uint16) (*VTcpConn, error) {
	remoteAddrPort := netip.AddrPortFrom(addr, port)
	localAddrPort := netip.AddrPortFrom(d.Interfaces["if0"].Ip, 10000)
	// TODO change port later to be random
	conn := d.CreateSocket(remoteAddrPort, localAddrPort.Port())
	synPacket := conn.CreateTcpPacket(conn.tcb.initialSeq, conn.tcb.initialAck, SYN, make([]byte, 1))
	// Send SYN
	_, err := d.SendTcp(conn.remoteAddr.Addr(), synPacket)
	if err != nil {
		return nil, err
	}
	// Wait for SYN + ACK
	select {
	case synAckPacket := <-conn.listenChannel:
		// TODO check if it is SYN + ACK
		conn.tcb.initialAck = synAckPacket.TcpHdr.SeqNum
		conn.tcb.currAck = synAckPacket.TcpHdr.SeqNum + 1
		ackPacket := conn.CreateTcpPacket(conn.tcb.currAck, conn.tcb.currSeq, ACK, make([]byte, 1))
		// Send Ack back
		_, err := d.SendTcp(conn.remoteAddr.Addr(), ackPacket)
		if err != nil {
			return nil, err
		}
		// TIMEOUT THREE ACKS HERE
	}

	key := SocketKey{remote: remoteAddrPort, host: localAddrPort, trasportType: tcp}
	d.connTable[key] = conn
	return &VTcpConn{}, nil
}

func (conn *VTcpConn) VRead(buf []byte) (int, error) {
	return 0, nil
}

func (conn *VTcpConn) VWrite(data []byte) (int, error) {
	return 0, nil
}

func (conn *VTcpConn) VClose() error {
	return nil
}

// TODO should make this private later
// TODO see data OFFSET and windows size
func (conn *VTcpConn) CreateTcpPacket(ackNum uint32, seqNum uint32, flags uint8, payload []byte) tcpheader.TcpPacket {
	hdr := header.TCPFields{
		SrcPort:       conn.localAddr.Port(),
		DstPort:       conn.remoteAddr.Port(),
		SeqNum:        seqNum,
		AckNum:        ackNum,
		DataOffset:    tcpheader.TcpHeaderLen, // TODO see here
		Flags:         flags,
		WindowSize:    conn.tcb.advertiseWindowSize(),
		Checksum:      0,
		UrgentPointer: 0,
	}
	checksum := tcpheader.ComputeTCPChecksum(&hdr, conn.localAddr.Addr(), conn.remoteAddr.Addr(), payload)
	hdr.Checksum = checksum
	println(tcpheader.TCPFieldsToString(&hdr))
	return tcpheader.TcpPacket{TcpHdr: hdr, Payload: payload}
}

// TODO should make this private later
// TODO check if this is correct way to communicate with lower layer
func (d *Device) SendTcp(dst netip.Addr, tcpPacket tcpheader.TcpPacket) (int, error) {
	// Serialize the TCP header
	tcpHeaderBytes := make(header.TCP, tcpheader.TcpHeaderLen)
	tcpHeaderBytes.Encode(&tcpPacket.TcpHdr)

	// Combine the TCP header + payload into one byte array, which
	// becomes the payload of the IP packet
	ipPacketPayload := make([]byte, 0, len(tcpHeaderBytes)+len(tcpPacket.Payload))
	ipPacketPayload = append(ipPacketPayload, tcpHeaderBytes...)
	ipPacketPayload = append(ipPacketPayload, []byte(tcpPacket.Payload)...)
	return d.SendIP(dst, uint8(tcpheader.IpProtoTcp), ipPacketPayload)
}

func (conn *VTcpConn) GetRemote() netip.AddrPort {
	return conn.remoteAddr
}

func (conn *VTcpConn) GetLocal() netip.AddrPort {
	return conn.localAddr
}

// TODO CHANGE LATER
func (conn *VTcpConn) GetStatus() Status {
	return conn.status
}

/* End Normal Socket Api */

func SocketKeyFromSocketInterface(s Socket) SocketKey {
	return SocketKey{remote: s.GetRemote(), host: s.GetLocal(), trasportType: tcp}
}

func GetSocketStatusStr(s Socket) string {
	strMap := map[Status]string{
		Listen:      "Listen",
		CloseWait:   "CloseWait",
		Closed:      "Closed",
		Closing:     "Closing",
		Established: "Established",
		FinWait1:    "FinWait1",
		FinWait2:    "FinWait2",
		LastAck:     "LastAck",
		SynRecv:     "SynRecv",
		SynSent:     "SynSent",
		TimeWait:    "TimeWait",
	}
	return strMap[s.GetStatus()]
}
