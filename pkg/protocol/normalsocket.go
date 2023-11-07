package protocol

import (
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"math/rand"
	"net/netip"
	"time"

	ipv4header "github.com/brown-csci1680/iptcp-headers"
	"github.com/google/netstack/tcpip/header"
)

type VTcpConn struct {
	remoteAddr    netip.AddrPort
	localAddr     netip.AddrPort
	d             *Device
	listenChannel chan tcpheader.TcpPacket // TODO CHANGE LATER
	status        Status
	tcb           TCB
	signalChannel chan bool
}

/* Start Normal Socket Api */

// TODO Create socket should return an error is there are no sufficent resourceS?
func (d *Device) CreateSocket(remoteAddrPort netip.AddrPort, localPort uint16) *VTcpConn {
	// Create TCB later with a separate call
	return &VTcpConn{
		remoteAddr:    remoteAddrPort,
		localAddr:     netip.AddrPortFrom(d.Interfaces["if0"].Ip, localPort),
		d:             d,
		listenChannel: make(chan tcpheader.TcpPacket),
		status:        Closed,
		signalChannel: make(chan bool),
	}
}

func (conn *VTcpConn) initializeTcb() {
	conn.tcb = *createTCB()
}

// For now do not choose a random port
// Should I account for a listen socket trying to initiate a connection?
func (d *Device) VConnect(addr netip.Addr, port uint16) (*VTcpConn, error) {
	remoteAddrPort := netip.AddrPortFrom(addr, port)
	// Ports above 20000
	randPort := uint16(rand.Uint32())%(^uint16(0)-20000) + 20000
	localAddrPort := netip.AddrPortFrom(d.Interfaces["if0"].Ip, randPort)
	if !remoteAddrPort.IsValid() {
		return nil, errInvalidIp
	}
	conn := d.CreateSocket(remoteAddrPort, localAddrPort.Port())
	conn.initializeTcb()
	key := SocketKeyFromSocketInterface(conn)
	conn.d.ConnTable[key] = conn
	_, err := conn.sendSyn()
	conn.status = SynSent
	if err != nil {
		return nil, err
	}
	// Wait for SYN + ACK
	select {
	case <-conn.signalChannel:
		return conn, nil
	// FOR now just 3 seconds timeout change later
	case <-time.NewTimer(time.Duration(time.Second * 3)).C:
		// CALL VCLOSE
		// TIMEOUT THREE ACKS HERE
		println("timeout")
		conn.VClose()
		return nil, errTimeout
	}
}

// Check rfc for all edge cases
func (conn *VTcpConn) VRead(buf []byte) (int, error) {
	bytesRead := 0
	switch conn.status {
	case Established:
		// Handler in the background adds to rcvBuf
		dataRead := conn.tcb.readFromRecv(uint32(len(buf)))
		copy(buf[:len(dataRead)], dataRead)
		bytesRead += len(dataRead)
	}
	return bytesRead, nil
}

func (conn *VTcpConn) VWrite(data []byte) (int, error) {
	// Maybe just split up payload to be smaller the segment size here
	bytesSend := 0
	// Segmenting data to be <= MSS
	// Let tcb see if it can send that amount of data or not and block
	for i := 0; i < len(data); i += int(Mss) {
		segData := data[i:min(i+int(Mss), len(data))]
		switch conn.status {
		case Established:
			conn.tcb.add2Send(conn.signalChannel, segData)
			// Data send should always be <=Mss size if segData < MSS
			dataSend := conn.tcb.readFromSend()
			n, err := conn.send(conn.tcb.sendNxt, conn.tcb.rcvNxt, ACK, dataSend)
			bytesSend += n
			if err != nil {
				return bytesSend, err
			}
		case SynSent:
			// Queue the data for transmission after entering ESTABLISHED state.
			//If no space to queue, respond with "error: insufficient resources".
		case CloseWait:
			// Segmentize the buffer and send it with a piggybacked acknowledgment (acknowledgment value = RCV.NXT).
			// If there is insufficient space to remember this buffer, simply return "error: insufficient resources".
		case TimeWait:
			return 0, errClosing
		}
	}
	return bytesSend, nil
}

// For now just say closed
func (conn *VTcpConn) VClose() error {
	conn.status = Closed
	return nil
}

// TODO should make this private later
// TODO see data OFFSET and windows size
func (conn *VTcpConn) CreateTcpPacket(seqNum uint32, ackNum uint32, flags uint8, payload []byte) tcpheader.TcpPacket {
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
	// println(tcpheader.TCPFieldsToString(&hdr))
	return tcpheader.TcpPacket{TcpHdr: hdr, Payload: payload}
}

func (conn *VTcpConn) SendSynchronized(payload []byte) {

}

func (d *Device) SendTcp(dst netip.Addr, tcpPacket tcpheader.TcpPacket) (int, error) {
	// Serialize the TCP header
	tcpHeaderBytes := make(header.TCP, tcpheader.TcpHeaderLen)
	tcpHeaderBytes.Encode(&tcpPacket.TcpHdr)

	// Combine the TCP header + payload into one byte array, which
	// becomes the payload of the IP packet
	ipPacketPayload := make([]byte, 0, len(tcpHeaderBytes)+len(tcpPacket.Payload))
	ipPacketPayload = append(ipPacketPayload, tcpHeaderBytes...)
	ipPacketPayload = append(ipPacketPayload, []byte(tcpPacket.Payload)...)
	n, err := d.SendIP(dst, uint8(tcpheader.IpProtoTcp), ipPacketPayload)
	n -= tcpheader.TcpHeaderLen + ipv4header.HeaderLen
	return n, err
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

// general send
func (conn *VTcpConn) send(seqNum uint32, ackNum uint32, flags uint8, payload []byte) (int, error) {
	packet := conn.CreateTcpPacket(seqNum, ackNum, flags, payload)
	return conn.d.SendTcp(conn.remoteAddr.Addr(), packet)
}

// Just signal sending, no payload
func (conn *VTcpConn) sendFlags(seqNum uint32, ackNum uint32, flags uint8) (int, error) {
	return conn.send(seqNum, ackNum, flags, make([]byte, 0))
}

func (conn *VTcpConn) sendAck(seqNum uint32, ackNum uint32) (int, error) {
	return conn.sendFlags(seqNum, ackNum, ACK)
}

func (conn *VTcpConn) sendSyn() (int, error) {
	return conn.sendFlags(conn.tcb.iss, conn.tcb.irs, SYN)
}

func (conn *VTcpConn) sendRst(ackNum uint32) (int, error) {
	rstPacket := conn.CreateTcpPacket(0, ackNum, RST, make([]byte, 0))
	return conn.d.SendTcp(conn.remoteAddr.Addr(), rstPacket)
}

// Ack is correct if it references values whithin the bounds of ISS and SND.NXT pointer
func (conn *VTcpConn) isAckCorrectBound(ackNum uint32) bool {
	return !(ackNum <= conn.tcb.iss || ackNum > uint32(conn.tcb.sendNxt))
}

// TODO ignoring edge case when number overflows or sequence wraps
func (conn *VTcpConn) isAckValid(ackNum uint32) bool {
	return conn.tcb.sendUna < ackNum && ackNum <= uint32(conn.tcb.sendNxt)
}

/* End Normal Socket Api */
