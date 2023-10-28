package protocol

import (
	"errors"
	"fmt"
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"
)

var (
	errPortInUse = errors.New("port already in use")
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

type Socket interface {
	getRemote() netip.AddrPort
	getLocal() netip.AddrPort
	getStatus() Status
}

type SocketKey struct {
	remote       netip.AddrPort
	host         netip.AddrPort
	trasportType Transport
}

type VTcpListener struct {
	remoteAddr netip.AddrPort
	localAddr  netip.AddrPort
}

type VTcpConn struct {
	remoteAddr netip.AddrPort
	localAddr  netip.AddrPort
	sendBuf    []byte
	recvBuf    []byte
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
	// DO something with the payload
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
	ln := &VTcpListener{remoteAddr: remoteAddrPort, localAddr: localAddrPort}
	d.listenTable[key] = ln

	return ln, nil
}
 
func (ln *VTcpListener) VAccept() (*VTcpConn, error) {
	return &VTcpConn{}, nil
}

func (ln *VTcpListener) VClose() error {
	return nil
}

func (ln *VTcpListener) getRemote() netip.AddrPort {
	return ln.remoteAddr
}

func (ln *VTcpListener) getLocal() netip.AddrPort {
	return ln.localAddr
}

func (ln *VTcpListener) getStatus() Status {
	return Listen
}

func listen(ln *VTcpListener, remoteChan chan netip.AddrPort) {
	for {
		remoteAddrPort := <-remoteChan

	}
}

/* End Listen Socket Api */
