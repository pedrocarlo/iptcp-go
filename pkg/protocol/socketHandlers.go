package protocol

import (
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"
)

// Handles receiving packets on normal socket
func handleConnStatus(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	var err error = nil
	switch conn.status {
	case SynSent:
		err = handleSynSentState(conn, tcpPacket)
	case SynRecv:
		err = handleSynRecvState(conn, tcpPacket)
	}
	return err
}

func handleListenState(ln *VTcpListener, tcpPacket *tcpheader.TcpPacket, remoteAddr netip.Addr, localAddr netip.Addr) error {
	flags := tcpPacket.TcpHdr.Flags
	remoteAddrPort := netip.AddrPortFrom(remoteAddr, tcpPacket.TcpHdr.SrcPort)
	// Do nothing
	if flags&RST == RST {
		return nil
	} else if flags&ACK == ACK {
		// Create a temporary conn to send reset
		conn := ln.d.CreateSocket(remoteAddrPort, ln.localAddr.Port())
		_, err := conn.sendRst(tcpPacket.TcpHdr.AckNum)
		return err
	} else if flags&SYN == SYN {
		// Just delegate to VAccept
		keyPacket := KeyPacket{
			tcpPacket: *tcpPacket,
			key: SocketKey{
				remote:       remoteAddrPort,
				local:        netip.AddrPortFrom(localAddr, ln.localAddr.Port()),
				trasportType: tcp},
		}
		// Sending signal to VAccept to run
		ln.listenChannel <- keyPacket
		return nil
	}
	return nil
}

// TODO ask professor how to handle receiving just a SYN here without ACK
func handleSynSentState(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	flags := tcpPacket.TcpHdr.Flags
	hdr := tcpPacket.TcpHdr
	if flags&ACK == ACK {
		if !conn.isAckCorrectBound(hdr.AckNum) {
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
	// Bad design should try to process the SYN + ACK on the first part
	if flags&SYN == SYN {
		conn.tcb.rcvNxt = hdr.SeqNum + 1
		conn.tcb.irs = hdr.SeqNum
		if flags&ACK == ACK {
			conn.tcb.sendUna = hdr.AckNum
			// segments on the retransmission queue that are thereby acknowledged
			// should be removed
			conn.status = Established
			conn.signalChannel <- true
			_, err := conn.sendAck(uint32(conn.tcb.sendNxt), uint32(conn.tcb.rcvNxt))
			return err
		}
		// Becomes the listener now?
		conn.sendFlags(conn.tcb.iss, uint32(conn.tcb.rcvNxt), SYN+ACK)
		conn.status = SynRecv
		
		// TODO
		// Set the variables:
		// SND.WND <- SEG.WND
		// SND.WL1 <- SEG.SEQ
		// SND.WL2 <- SEG.ACK
	}
	conn.signalChannel <- true
	return nil
}

// Came from Active Open
func handleSynRecvState(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	flags := tcpPacket.TcpHdr.Flags
	hdr := tcpPacket.TcpHdr
	if flags&RST == RST {
		conn.signalChannel <- false
		conn.VClose()
		conn.status = Closed
		return errConnectionReset
	}
	// TODO WHAT TO DO HERE DO NOT UNDERSTAND WHAT RFC WANTS
	if flags&SYN == SYN {
		// I do not think we follow RFC 5961
		// If SYN in the window -> send RST
		// Connection reset flush everything else
		return nil
	}
	if flags&ACK == ACK {
		if conn.isAckValid(tcpPacket.TcpHdr.AckNum) {
			conn.status = Established
			/*
				SND.WND <- SEG.WND
				SND.WL1 <- SEG.SEQ
				SND.WL2 <- SEG.ACK */
			conn.signalChannel <- true
		} else {
			println("sending reset synrecv")
			conn.sendRst(hdr.AckNum)
			conn.signalChannel <- false
		}
	}
	return nil
}
