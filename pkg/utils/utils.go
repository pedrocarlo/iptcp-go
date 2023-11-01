package utils

import (
	"bufio"
	"fmt"
	protocol "iptcp-pedrocarlo/pkg/protocol"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
)

type Command func(*protocol.Device, []string, *SocketIds)
type CommandMap map[string]Command
type SocketIds map[uint]protocol.Socket

func initialize() CommandMap {
	commandMap := make(CommandMap)
	commandMap["exit"] = Exit
	commandMap["li"] = ListInterfaces
	commandMap["ln"] = ListNeighbours
	commandMap["lr"] = ListNeighbours
	commandMap["lr"] = ListRoutes
	commandMap["up"] = UpInterface
	commandMap["down"] = DownInterface
	commandMap["send"] = SendMessage
	commandMap["rip"] = SendTestRip
	commandMap["a"] = ListenPort
	commandMap["c"] = ConnectPort
	commandMap["ls"] = ListSockets

	return commandMap
}

func Repl(d *protocol.Device) {
	commandMap := initialize()

	keys := make([]string, 0, len(commandMap))
	for k := range commandMap {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	socketIds := make(SocketIds)

	repl(d, commandMap, keys, socketIds)
}

func repl(d *protocol.Device, commandMap CommandMap, sortedKeys []string, socketIds SocketIds) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("> ")
		text, _ := reader.ReadString('\n')
		text = strings.Trim(text, "\n")
		args := strings.Split(text, " ")
		command, ok := commandMap[args[0]]
		if text == "" {
			for _, k := range sortedKeys {
				println(k)
			}
			continue
		}
		if ok {
			go command(d, args[1:], &socketIds)
		} else {
			fmt.Printf("Command '%s' not found\n", args[0])
		}
	}
}

func Exit(d *protocol.Device, _ []string, _ *SocketIds) {
	os.Exit(0)
}

func ListInterfaces(d *protocol.Device, _ []string, _ *SocketIds) {
	interfaces := d.Interfaces
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "Name\tAddr/Prefix\tState\t")
	for name, inter := range interfaces {
		state := ""
		if inter.IsUp {
			state = "up"
		} else {
			state = "down"
		}
		fmt.Fprintf(w, "%s\t%s/%d\t%s\t\n", name, inter.Ip, inter.Prefix.Bits(), state)
	}
	w.Flush()
}

func ListNeighbours(d *protocol.Device, _ []string, _ *SocketIds) {
	neighbours := d.Neighbours
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "Iface\tVIP\tUDPAddr\t")
	for _, n := range neighbours {
		fmt.Fprintf(w, "%s\t%s\t%s\t\n", n.InterfaceName, n.Ip.String(), n.UdpPort.String())
	}
	w.Flush()
}

func ListRoutes(d *protocol.Device, _ []string, _ *SocketIds) {
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "T\tPrefix\tNext hop\tCost\t")
	// Mutex here stuff
	d.Mutex.Lock()
	for pre, hop := range d.Table {
		var t string
		addr := hop.Addr.String()
		if pre.Bits() == 0 {
			t = "S"
		} else if hop.Cost == 0 {
			t = "L"
			addr = "LOCAL:"
			for iface, inter := range d.Interfaces {
				if inter.Prefix == pre {
					addr += iface
				}
			}
		} else {
			t = "R"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t\n", t, pre, addr, hop.Cost)
	}
	d.Mutex.Unlock()
	w.Flush()
}

func UpInterface(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 1 {
		println("up <ifname>")
		return
	}
	name := args[0]
	d.Mutex.Lock()
	inter, ok := d.Interfaces[name]
	if ok {
		inter.IsUp = true
	}
	d.Mutex.Unlock()
}

func DownInterface(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 1 {
		println("up <ifname>")
		return
	}
	name := args[0]
	d.Mutex.Lock()
	inter, ok := d.Interfaces[name]
	if ok {
		inter.IsUp = false
	}
	d.Mutex.Unlock()
}

func SendMessage(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 2 {
		println("send <addr> <message>")
		return
	}
	addr, msg := args[0], args[1]
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		println(err)
		return
	}
	n, _ := d.SendIP(addrIp, 0, []byte(msg))
	fmt.Printf("Sent %d bytes\n", n)
}

func SendTestRip(d *protocol.Device, args []string, _ *SocketIds) {
	if len(args) < 1 {
		println("usage: a <port>")
		return
	}
	addr := args[0]
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		fmt.Printf("Sent %d bytes\n", 0)
		return
	}
	h, err := d.CreateRipPacket(2, addrIp, d.Table)
	if err != nil {
		fmt.Printf("Sent %d bytes\n", 0)
		return
	}
	h.PrintHeader()
	marshal, err := h.Marshal()
	if err != nil {
		fmt.Printf("Sent %d bytes\n", 0)
		return
	}
	n, _ := d.SendIP(addrIp, 200, marshal)
	fmt.Printf("Sent %d bytes\n", n)
}

func addToSocketIds(socketIds *SocketIds, socket protocol.Socket) {
	idToAdd := uint(0)
	// Could wrap around after 2^32 entries or more but not really of concern here
	// Not expecting that many sockets
	for id := range *socketIds {
		idToAdd = id + 1
	}
	(*socketIds)[idToAdd] = socket
}

// Maybe have a goroutine to remove entries that are closed
func removeFromSocketIds(socketIds *SocketIds) {

}

func ListenPort(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 1 {
		println("a <port>")
		return
	}
	portStr := args[0]
	port, err := strconv.Atoi(portStr)
	if err != nil {
		println(err.Error())
		return
	}
	ln, err := d.VListen(uint16(port))
	if err != nil {
		println(err.Error())
		return
	}
	addToSocketIds(socketIds, ln)
	for {
		// Just listen and accept
		conn, err := ln.VAccept()
		if err != nil {
			continue
		}
		// TODO ADD TO REPL SOCKET TABLE
		// Debugging
		fmt.Printf("Accepted %s", conn.GetRemote())
		addToSocketIds(socketIds, conn)
	}
}

func ConnectPort(d *protocol.Device, args []string, socketIds *SocketIds) {
	if len(args) < 2 {
		println("usage: c <vip> <port>")
		return
	}
	addr, portStr := args[0], args[1]
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		println(err)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		println(err)
		return
	}
	conn, err := d.VConnect(addrIp, uint16(port))
	if err != nil {
		println(err)
		return
	}
	addToSocketIds(socketIds, conn)
}

func ListSockets(d *protocol.Device, _ []string, socketIds *SocketIds) {
	w := tabwriter.NewWriter(os.Stdout, 0, 10, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(w, "SID\tLAddr\tLPort\tRAddr\tRPort\tStatus\t\n")
	for id, socket := range *socketIds {
		remoteAddrPort := socket.GetRemote()
		localAddrPort := socket.GetLocal()
		fmt.Fprintf(
			w,
			"%d\t%s\t%d\t%s\t%d\t%s\n",
			id,
			remoteAddrPort.Addr(),
			remoteAddrPort.Port(),
			localAddrPort.Addr(),
			localAddrPort.Port(),
			protocol.GetSocketStatusStr(socket))
	}
	w.Flush()
}
