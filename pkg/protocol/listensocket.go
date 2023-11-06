package protocol

import (
	"errors"
	"fmt"
	tcpheader "iptcp-pedrocarlo/pkg/tcp-headers"
	"net/netip"
	"time"
)

type VTcpListener struct {
	remoteAddr    netip.AddrPort
	localAddr     netip.AddrPort
	d             *Device
	listenChannel chan KeyPacket // Send remote Addr
	status        Status
}

type KeyPacket struct {
	tcpPacket tcpheader.TcpPacket
	key       SocketKey
}

/* Start Listen Socket Api */

// TODO will add tables to device in protocol but see if it is best approach
func (d *Device) VListen(port uint16) (*VTcpListener, error) {
	// TODO check if it is okay to use must parse here
	remoteAddrPort := netip.MustParseAddrPort("0.0.0.0:0")
	localAddrPort := netip.MustParseAddrPort(fmt.Sprintf("0.0.0.0:%d", port))
	key := SocketKey{remote: remoteAddrPort, host: localAddrPort, trasportType: tcp}
	// Check in table
	_, ok := d.ListenTable[key]
	if ok {
		return nil, errPortInUse
	}
	// Spawn a thread
	ln := &VTcpListener{remoteAddr: remoteAddrPort, localAddr: localAddrPort, d: d, listenChannel: make(chan KeyPacket), status: Listen}
	d.ListenTable[key] = ln
	return ln, nil
}

// See later timeout here if does not receive second part of the handshake
func (ln *VTcpListener) VAccept() (*VTcpConn, error) {
	keyPacket := <-ln.listenChannel
	key, synPacket := keyPacket.key, keyPacket.tcpPacket
	err := checkFlagsAccept(synPacket.TcpHdr.Flags)
	conn := ln.d.CreateSocket(key.remote, key.host.Port())
	conn.tcb.irs = synPacket.TcpHdr.SeqNum
	if err != nil {
		// TODO RFC 3.10.7.2 Listen State ACK
		if errors.Is(err, errInvalidAck) {
			rstPacket := conn.CreateTcpPacket(0, synPacket.TcpHdr.AckNum, RST, make([]byte, 0))
			conn.d.SendTcp(conn.remoteAddr.Addr(), rstPacket)
			return nil, err
		}
		return nil, err
	}
	ln.d.ConnTable[key] = conn
	conn.status = SynRecv
	// Send Syn Ack
	conn.tcb.setSynReceivedState()
	tcpPacket := conn.CreateTcpPacket(conn.tcb.iss, uint32(conn.tcb.rcvNxt), SYN+ACK, make([]byte, 0))
	_, err = conn.d.SendTcp(conn.remoteAddr.Addr(), tcpPacket)
	if err != nil {
		return nil, err
	}
	conn.tcb.setSynSentState()
	conn.status = SynSent

	conn.d.ConnTable[key] = conn
	// Wait for Ack from channel else timeout
	select {
	case tcpPacket := <-conn.listenChannel:
		// Call Ack with segment
		if tcpPacket.TcpHdr.Flags == ACK {
			conn.status = Established
		} else {
			// Error out or send reset
		}
		// TODO Make sure that it is an ACK
		// TODO refactor this to be a separate function
	case <-time.NewTimer(time.Second).C:
		// Call Vclose
		conn.VClose()
		return nil, errTimeout
	}
	return conn, nil
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
	return ln.status
}

func checkFlagsAccept(flags uint8) error {
	var err error
	// Contains this flag
	if flags&RST == RST {
		err = errInvalidRst
	} else if flags&ACK == ACK {
		err = errInvalidAck
	} else if flags&SYN == SYN {
		err = nil
	} else {
		err = errInvalidFlag
	}
	return err
}

/* End Listen Socket Api */
