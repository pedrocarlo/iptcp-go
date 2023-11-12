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
	case Established:
		err = handleEstablished(conn, tcpPacket)
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
		if !conn.isAckAcceptable(hdr.AckNum) {
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
		conn.tcb.rcvLbr = conn.tcb.rcvNxt
		conn.tcb.irs = hdr.SeqNum
		conn.tcb.sendWnd = hdr.WindowSize
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
		if conn.isAckAcceptable(tcpPacket.TcpHdr.AckNum) {
			conn.status = Established
			conn.tcb.sendWnd = hdr.WindowSize
			conn.tcb.sendWl1 = hdr.SeqNum
			conn.tcb.sendWl2 = hdr.AckNum
			conn.signalChannel <- true
		} else {
			println("sending reset synrecv")
			conn.sendRst(hdr.AckNum)
			conn.signalChannel <- false
		}
	}
	return nil
}

// Section 3.4 retransmission of acceptable ack
// For now just focus on ACK
// TODO Check for SYN BIT AND RST
func handleEstablished(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	flags := tcpPacket.TcpHdr.Flags
	hdr := tcpPacket.TcpHdr
	conn.tcb.sendWnd = hdr.WindowSize
	ackNum := hdr.SeqNum
	var err error = nil
	// Should probably check for rst before ack
	// Be mindful of early arrivals and see what to do with them
	if conn.isSegmentAcceptable(hdr.SeqNum, uint32(len(tcpPacket.Payload))) {
		// TODO Check RST later
		if flags&RST == RST {
			if hdr.SeqNum == conn.tcb.rcvNxt {
				conn.sendRst(ackNum)
				conn.VClose()
				// Maybe see later
				return errConnectionReset
			}
		}
		if flags&ACK == ACK {
			// SND.UNA < SEG.ACK =< SND.NXT
			if conn.isAckAcceptable(hdr.AckNum) {
				// conn.tcb.sendUna = hdr.AckNum
				// Acknowledge segments in retransmission queue
				//
				if len(tcpPacket.Payload) > 0 {
					conn.tcb.AddRead(tcpPacket.Payload)
					_, err = conn.sendAck(conn.tcb.sendNxt, ackNum+uint32(len(tcpPacket.Payload)))
				}
				// TODO see what should be correct SeqNum here
			} else if hdr.AckNum <= conn.tcb.sendUna {
				return nil
			} else if hdr.AckNum > conn.tcb.sendNxt {
				_, err = conn.sendAck(conn.tcb.sendNxt, conn.tcb.rcvNxt)
				return err
			} else if conn.tcb.sendUna <= hdr.AckNum && hdr.AckNum <= conn.tcb.sendNxt {
				// Update sendWnd
				wl1, wl2 := conn.tcb.sendWl1, conn.tcb.sendWl2
				if wl1 < hdr.SeqNum || (wl1 == hdr.SeqNum && wl2 <= hdr.AckNum) {
					conn.tcb.sendWnd = hdr.WindowSize
					conn.tcb.sendWl1 = hdr.SeqNum
					conn.tcb.sendWl2 = hdr.AckNum
					// Notify sendWnd changed
					select {
					case conn.tcb.windowSendSignal <- conn.tcb.sendWnd:
					default:
					}
				}
			}
			return err
		} else {
			// Send Reset I think
			// Zero windows probing with len == 1 of data
			// Special Allowance for Valid ACks and RST
		}
	} else {
		if flags&RST == RST {
			return nil
		}
		_, err = conn.send(conn.tcb.sendNxt, conn.tcb.rcvNxt, ACK, make([]byte, 0))
		return err
	}
	return err
}
