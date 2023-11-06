package protocol

import (
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"
	"time"

	"github.com/google/netstack/tcpip/header"
)

type VTcpConn struct {
	remoteAddr    netip.AddrPort
	localAddr     netip.AddrPort
	d             *Device
	listenChannel chan tcpheader.TcpPacket // TODO CHANGE LATER
	status        Status
	tcb           TCB
}

/* Start Normal Socket Api */

// TODO Create socket should return an error is there are no sufficent resourceS?
func (d *Device) CreateSocket(remoteAddrPort netip.AddrPort, localPort uint16) *VTcpConn {
	return &VTcpConn{
		remoteAddr:    remoteAddrPort,
		localAddr:     netip.AddrPortFrom(d.Interfaces["if0"].Ip, localPort),
		d:             d,
		listenChannel: make(chan tcpheader.TcpPacket),
		status:        Closed,
		tcb:           *createTCB()}
}

// For now do not choose a random port
// Should I account for a listen socket trying to initiate a connection?
func (d *Device) VConnect(addr netip.Addr, port uint16) (*VTcpConn, error) {
	remoteAddrPort := netip.AddrPortFrom(addr, port)
	localAddrPort := netip.AddrPortFrom(d.Interfaces["if0"].Ip, 10000)
	if !remoteAddrPort.IsValid() {
		return nil, errInvalidIp
	}
	// TODO change port later to be random
	conn := d.CreateSocket(remoteAddrPort, localAddrPort.Port())
	key := SocketKeyFromSocketInterface(conn)
	conn.d.ConnTable[key] = conn
	_, err := conn.sendSyn()
	conn.status = SynSent
	if err != nil {
		return nil, err
	}
	// Wait for SYN + ACK
	select {
	case synAckPacket := <-conn.listenChannel:
		// TODO check if it is SYN + ACK
		_, err := conn.sendFirstAck(synAckPacket)
		if err != nil {
			return nil, err
		}
		conn.status = Established
	// FOR now just 3 seconds timeout change later
	case <-time.NewTimer(time.Duration(time.Second * 3)).C:
		// CALL VCLOSE
		// TIMEOUT THREE ACKS HERE
		conn.VClose()
		return nil, errTimeout
	}
	return conn, nil
}

// Check rfc for all edge cases
func (conn *VTcpConn) VRead(buf []byte) (int, error) {
	return 0, nil
}

func (conn *VTcpConn) VWrite(data []byte) (int, error) {
	switch conn.status {
	case SynSent:
		// Queue the data for transmission after entering ESTABLISHED state.
		//If no space to queue, respond with "error: insufficient resources".
	case CloseWait:
		// Segmentize the buffer and send it with a piggybacked acknowledgment (acknowledgment value = RCV.NXT).
		// If there is insufficient space to remember this buffer, simply return "error: insufficient resources".
	case TimeWait:
		return 0, errClosing
	}
	return 0, nil
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

// TODO should make this private later
// TODO check if this is correct way to communicate with lower layer

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

// TODO ask professor how to handle receiving just a SYN here without ACK
func (conn *VTcpConn) handleSynSentState(tcpPacket tcpheader.TcpPacket) error {
	flags := tcpPacket.TcpHdr.Flags
	hdr := tcpPacket.TcpHdr
	if flags&ACK == ACK {
		if !conn.isAckCorrect(hdr.AckNum) {
			conn.sendRst(hdr.AckNum)
			return errIncorrectAck
		}
		if !conn.isAckValid(hdr.AckNum) {
			return errInvalidAck
		}
		// ACK + RST
		if flags&RST == RST {
			conn.VClose()
			return errConnectionReset
		}
	}
	// Bad design should try to process the SYN ACK on the first part
	if flags&SYN == SYN {
		conn.tcb.rcvNxt = uint(hdr.SeqNum) + 1
		conn.tcb.irs = hdr.SeqNum
		if flags&ACK == ACK {
			conn.tcb.sendUna = uint(hdr.AckNum)
			// segments on the retransmission queue that are thereby acknowledged
			// should be removed
			conn.status = Established
			_, err := conn.sendAck(uint32(conn.tcb.sendNxt), uint32(conn.tcb.rcvNxt))
			return err
		}
		conn.sendFlags(conn.tcb.iss, uint32(conn.tcb.rcvNxt), SYN+ACK)
		conn.status = SynRecv
		// TODO
		// Set the variables:
		// SND.WND <- SEG.WND
		// SND.WL1 <- SEG.SEQ
		// SND.WL2 <- SEG.ACK
	}
	return nil
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

// TODO see if status should be changed here
func (conn *VTcpConn) sendSyn() (int, error) {
	conn.tcb.setSynSentState()
	// Send SYN
	return conn.sendFlags(conn.tcb.iss, conn.tcb.irs, SYN)
}

func (conn *VTcpConn) sendFirstAck(synAckPacket tcpheader.TcpPacket) (int, error) {
	conn.tcb.irs = synAckPacket.TcpHdr.SeqNum
	// conn.tcb.currAck = synAckPacket.TcpHdr.SeqNum + 1
	// Set first ack state
	ackPacket := conn.CreateTcpPacket(uint32(conn.tcb.sendNxt), uint32(conn.tcb.rcvNxt), ACK, make([]byte, 0))
	// Send Ack back
	return conn.d.SendTcp(conn.remoteAddr.Addr(), ackPacket)
}

func (conn *VTcpConn) sendRst(ackNum uint32) {
	rstPacket := conn.CreateTcpPacket(0, ackNum, RST, make([]byte, 0))
	conn.d.SendTcp(conn.remoteAddr.Addr(), rstPacket)
}

// Ack is correct if it references values whithin the bounds of ISS and SND.NXT pointer
func (conn *VTcpConn) isAckCorrect(ackNum uint32) bool {
	return !(ackNum <= conn.tcb.iss || ackNum > uint32(conn.tcb.sendNxt))
}

func (conn *VTcpConn) isAckValid(ackNum uint32) bool {
	return conn.tcb.sendUna < uint(ackNum) && ackNum <= uint32(conn.tcb.sendNxt)
}

/* End Normal Socket Api */

func (conn *VTcpConn) resetIfAck(tcpPacket tcpheader.TcpPacket) {

}
