package protocol

import (
	"errors"
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
	"github.com/google/netstack/tcpip/header"
)

var (
	errPortInUse       = errors.New("port already in use")
	errTimeout         = errors.New("connection timed out")
	errInvalidIp       = errors.New("remote socket unspecified, ip is not valid")
	errClosing         = errors.New("connection closing")
	errInvalidRst      = errors.New("invalid reset received")
	errInvalidFlag     = errors.New("invalid flag received")
	errInvalidAck      = errors.New("invalid ack received")
	errIncorrectAck    = errors.New("incorrect ack number received")
	errConnectionReset = errors.New("reset flag received, connection reset")
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
	SYN  = header.TCPFlagSyn
	ACK  = header.TCPFlagAck
	RST  = header.TCPFlagRst
	FIN  = header.TCPFlagFin
	URG  = header.TCPFlagUrg
	PUSH = header.TCPFlagPsh
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

type ListenTable map[SocketKey]*VTcpListener
type ConnTable map[SocketKey]*VTcpConn

func tcpHandler(d *Device, p *Packet, _ []interface{}) {
	tcpHdr := tcpheader.ParseTCPHeader(p.Data)
	tcpPayload := p.Data[tcpHdr.DataOffset:]
	// tcpChecksumFromHeader := tcpHdr.Checksum
	tcpHdr.Checksum = 0
	// tcpComputedChecksum := tcpheader.ComputeTCPChecksum(&tcpHdr, p.Header.Src, p.Header.Dst, tcpPayload)

	// TODO CHECKSUM LATER

	// if tcpChecksumFromHeader != tcpComputedChecksum {
	// 	return
	// }
	tcpPacket := tcpheader.TcpPacket{TcpHdr: tcpHdr, Payload: tcpPayload}
	handlerFlags(d, p, &tcpPacket)
}

func handlerFlags(d *Device, p *Packet, tcpPacket *tcpheader.TcpPacket) {
	key := SocketKeyFromPacketAndTcpHdr(p.Header, tcpPacket.TcpHdr)
	listenKey := SocketKeyDefaultListen(tcpPacket.TcpHdr)
	tcpHdr := tcpPacket.TcpHdr
	switch tcpHdr.Flags {
	default:
		conn, ok := d.ConnTable[key]
		ln, lnok := d.ListenTable[listenKey]
		if ok {
			conn.listenChannel <- *tcpPacket
		}
		if lnok {
			ln.listenChannel <- KeyPacket{key: key, tcpPacket: *tcpPacket}
		}
	}
}

func SocketKeyFromSocketInterface(s Socket) SocketKey {
	return SocketKey{remote: s.GetRemote(), host: s.GetLocal(), trasportType: tcp}
}

func SocketKeyFromPacketAndTcpHdr(ipHdr ipv4header.IPv4Header, tcpHdr header.TCPFields) SocketKey {
	return SocketKey{
		remote:       netip.AddrPortFrom(ipHdr.Src, tcpHdr.SrcPort),
		host:         netip.AddrPortFrom(ipHdr.Dst, tcpHdr.DstPort),
		trasportType: tcp,
	}
}
func SocketKeyDefaultListen(tcpHdr header.TCPFields) SocketKey {
	zeroIpAddr := netip.MustParseAddr("0.0.0.0")
	return SocketKey{
		remote:       netip.AddrPortFrom(zeroIpAddr, 0),
		host:         netip.AddrPortFrom(zeroIpAddr, tcpHdr.DstPort),
		trasportType: tcp,
	}
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

func (key *SocketKey) GetRemote() netip.AddrPort {
	return key.remote
}

func (key *SocketKey) GetLocal() netip.AddrPort {
	return key.host
}

/* Error Related Functions */
