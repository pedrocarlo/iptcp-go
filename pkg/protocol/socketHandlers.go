package protocol

import (
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"
)

// Handles receiving packets on normal socket
func handleConnStatus(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	var err error = nil
	switch conn.status {
	case Closed:
	case Listen:
	case SynSent:
		err = handleSynSentState(conn, tcpPacket)
	default:
		err = handleOtherStates(conn, tcpPacket)
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
	wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
	wrappedUna := conn.tcb.wrapFromIss(conn.tcb.sendUna)
	acceptable := false
	if flags&ACK == ACK {
		if hdr.AckNum <= conn.tcb.iss || hdr.AckNum > wrappedSendNxt {
			conn.sendRst(hdr.AckNum)
			return errIncorrectAck
		}
		if wrappedUna <= hdr.AckNum && hdr.AckNum <= wrappedSendNxt {
			acceptable = true
		}
		// ACK + RST
		if flags&RST == RST {
			if acceptable {
				conn.status = Closed
				conn.closeDelete()
				return errConnectionReset
			}
			// Drop segment
			return nil
		}
	}
	// Bad design should try to process the SYN + ACK on the first part
	if flags&SYN == SYN {
		conn.tcb.rcvNxt = 1
		conn.tcb.rcvLbr = conn.tcb.rcvNxt
		conn.tcb.irs = hdr.SeqNum
		if acceptable {
			dist := wrappedDist(conn.tcb.wrapFromIss(conn.tcb.sendUna), hdr.AckNum)
			conn.tcb.sendUna += uint64(dist)
			// segments on the retransmission queue that are thereby acknowledged
			// should be removed
		}
		wrappedUna := conn.tcb.wrapFromIss(conn.tcb.sendUna)
		if wrappedUna > conn.tcb.iss {
			conn.status = Established
			conn.signalChannel <- true
			conn.sendAck(conn.tcb.wrapFromIss(conn.tcb.sendNxt), conn.tcb.wrapFromIrs(conn.tcb.rcvNxt))
			conn.tcb.sendWnd = hdr.WindowSize // Maybe not necessary
			conn.tcb.sendWl1 = hdr.SeqNum
			conn.tcb.sendWl2 = hdr.AckNum
			return nil
		} else {
			// Becomes the listener now?
			conn.sendFlags(conn.tcb.iss, conn.tcb.wrapFromIrs(conn.tcb.rcvNxt), SYN+ACK)
			conn.status = SynRecv
		}
	}
	conn.signalChannel <- true
	return nil
}

// Came from Active Open
func handleSynRecvState(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	flags := tcpPacket.TcpHdr.Flags
	hdr := tcpPacket.TcpHdr
	wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
	wrappedSendUna := conn.tcb.wrapFromIss(conn.tcb.sendUna)
	if flags&RST == RST {
		conn.signalChannel <- false
		conn.closeDelete()
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
		err := handleAckOtherStates(conn, tcpPacket)
		if err != nil {
			return err
		}
		if wrappedSendUna <= hdr.AckNum && hdr.AckNum <= wrappedSendNxt {
			conn.status = Established
			conn.tcb.sendWnd = hdr.WindowSize
			conn.tcb.sendWl1 = hdr.SeqNum
			conn.tcb.sendWl2 = hdr.AckNum
		} else {
			println("sending reset synrecv")
			conn.sendRst(hdr.AckNum)
			conn.signalChannel <- false
		}
		conn.signalChannel <- true

	}
	return nil
}

// Section 3.4 retransmission of acceptable ack
// For now just focus on ACK
// TODO Check for SYN BIT AND RST
func handleOtherStates(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	flags := tcpPacket.TcpHdr.Flags
	hdr := tcpPacket.TcpHdr
	ackNum := hdr.SeqNum
	var err error = nil
	wrappedRcvNxt := conn.tcb.wrapFromIrs(conn.tcb.rcvNxt)
	wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
	wrappedSendUna := conn.tcb.wrapFromIss(conn.tcb.sendUna)
	// Should probably check for rst before ack
	// Be mindful of early arrivals and see what to do with them

	// See if this is correct
	if conn.isSegmentAcceptable(hdr.SeqNum, uint32(len(tcpPacket.Payload))) {
		// TODO Check RST later
		if flags&RST == RST {
			if hdr.SeqNum == wrappedRcvNxt {
				conn.sendRst(ackNum)
				conn.closeDelete()
				// Maybe see later
				return errConnectionReset
			}
		}
		if flags&ACK == ACK {
			if conn.status == SynRecv {
				if wrappedSendUna <= hdr.AckNum && hdr.AckNum <= wrappedSendNxt {
					conn.status = Established
					conn.signalChannel <- true
				}
			}
			err := handleAckOtherStates(conn, tcpPacket)
			if err != nil {
				// Dropping segment here
				return nil
			}
		}
		// Process the segment
		if len(tcpPacket.Payload) > 0 {
			conn.tcb.AddRead(tcpPacket.Payload)
			conn.sendAck(wrappedSendNxt, ackNum+uint32(len(tcpPacket.Payload)))
		}
		if flags&FIN == FIN {
			handleFinOtherStates(conn, tcpPacket)
			return nil
		}
	} else {
		if flags&RST == RST {
			return nil
		}
		// Window probe
		_, err = conn.send(wrappedSendNxt, wrappedRcvNxt, ACK, make([]byte, 0))
		return err
	}
	return err
}

// Different handling for TIMEWAIT AND LAST ACK STATE
func handleAckOtherStates(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) error {
	hdr := tcpPacket.TcpHdr
	wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
	wrappedSendUna := conn.tcb.wrapFromIss(conn.tcb.sendUna)
	// SND.UNA < SEG.ACK =< SND.NXT
	println("Curr Ack", hdr.AckNum)
	println("Curr SendNxt", wrappedSendNxt)
	if conn.isAckAcceptable(hdr.AckNum) {
		// Acknowledge segments in retransmission queue
		// Early arrivals put in a min heap for queing
		dist := wrappedDist(conn.tcb.wrapFromIss(conn.tcb.sendUna), hdr.AckNum)
		conn.tcb.sendUna += uint64(dist)
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
	} else if hdr.AckNum <= wrappedSendUna {
	} else if hdr.AckNum > wrappedSendNxt {
		conn.sendAck(wrappedSendNxt, wrappedSendNxt)
		// Drop Segment
		return errInvalidAck
	}
	switch conn.status {
	case FinWait1:
		if hdr.AckNum == wrappedSendNxt {
			conn.status = FinWait2
		}
	case Closing:
		if hdr.AckNum == wrappedSendNxt {
			conn.status = TimeWait
			// Set timewait timer somewhere
		}
	}
	return nil
}

func handleFinOtherStates(conn *VTcpConn, tcpPacket *tcpheader.TcpPacket) {
	hdr := tcpPacket.TcpHdr
	wrappedRcvNxt := conn.tcb.wrapFromIrs(conn.tcb.rcvNxt)
	wrappedSendNxt := conn.tcb.wrapFromIss(conn.tcb.sendNxt)
	println("Prev RCV NXT", wrappedRcvNxt)
	seqDist := wrappedDist(wrappedRcvNxt, hdr.SeqNum+1)
	conn.tcb.rcvNxt += uint64(seqDist)
	wrappedRcvNxt = conn.tcb.wrapFromIrs(conn.tcb.rcvNxt)
	println("After RCV NXT", wrappedRcvNxt)
	println("Seq Num + 1", hdr.SeqNum+1)

	conn.sendAck(wrappedSendNxt, wrappedRcvNxt)
	switch conn.status {
	case Established:
		conn.status = CloseWait
	case FinWait1:
		if hdr.AckNum == wrappedSendNxt {
			conn.status = TimeWait
			// Set a timeout for it
		} else {
			conn.status = Closing // TODO
		}
	case FinWait2:
		conn.status = TimeWait
		// Set a timeout for it
	case TimeWait:
		// restart 2MSL timer
	}

}
