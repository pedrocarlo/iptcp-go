package utils

import (
	"bufio"
	"fmt"
	protocol "iptcp-pedrocarlo/pkg"
	"net/netip"
	"os"
	"strings"
	"text/tabwriter"
)

func Repl(d *protocol.Device) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("> ")
		text, _ := reader.ReadString('\n')
		text = strings.Trim(text, "\n")
		if text == "q" {
			os.Exit(0)
		}
		if text == "li" {
			ListInterfaces(d)
		}
		if text == "ln" {
			ListNeighbours(d)
		}
		if text == "lr" {
			ListRoutes(d)
		}
		splitText := strings.Split(text, " ")

		if splitText[0] == "up" {
			if len(splitText) < 2 {
				println("Should pass more arguments for command")
				continue
			}
			UpInterface(d, splitText[1])
		}
		if splitText[0] == "down" {
			if len(splitText) < 2 {
				println("Should pass more arguments for command")
				continue
			}
			DownInterface(d, splitText[1])
		}
		if splitText[0] == "send" {
			if len(splitText) < 3 {
				println("Should pass more arguments for command")
				continue
			}
			n, err := SendMessage(d, splitText[1], strings.Join(splitText[2:], " "))
			if err != nil {
				println(err.Error())
			}
			fmt.Printf("Sent %d bytes\n", n)
		}
		if splitText[0] == "rip" {
			if len(splitText) < 2 {
				println("Should pass more arguments for command")
				continue
			}
			n, err := SendTestRip(d, splitText[1])
			if err != nil {
				println(err.Error())
			}
			fmt.Printf("Sent %d bytes\n", n)
		}
	}
}

func ListInterfaces(d *protocol.Device) {
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

func ListNeighbours(d *protocol.Device) {
	neighbours := d.Neighbours
	w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "Iface\tVIP\tUDPAddr\t")
	for _, n := range neighbours {
		fmt.Fprintf(w, "%s\t%s\t%s\t\n", n.InterfaceName, n.Ip.String(), n.UdpPort.String())
	}
	w.Flush()
}

func ListRoutes(d *protocol.Device) {
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

func UpInterface(d *protocol.Device, name string) {
	if !d.Interfaces[name].IsUp {
		// send rip package to all reouters
		d.Mutex.Lock()
		for _, router := range d.RipNeighbors {
			err := d.SendRip(protocol.RipResponse, router)
			if err != nil {
				continue
			}
		}
		d.Mutex.Unlock()
	}
	d.Interfaces[name].IsUp = true
}

func DownInterface(d *protocol.Device, name string) {
	d.Interfaces[name].IsUp = false
}

func SendMessage(d *protocol.Device, addr string, msg string) (int, error) {
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		return 0, err
	}
	n, err := d.SendIP(addrIp, 0, []byte(msg))
	return n, err
}

func SendTestRip(d *protocol.Device, addr string) (int, error) {
	addrIp, err := netip.ParseAddr(addr)
	if err != nil {
		return 0, err
	}
	h, err := d.CreateRipPacket(2, addrIp)
	if err != nil {
		return 0, err
	}
	h.PrintHeader()
	marshal, err := h.Marshal()
	if err != nil {
		return 0, err
	}
	n, err := d.SendIP(addrIp, 200, marshal)
	return n, err
}
