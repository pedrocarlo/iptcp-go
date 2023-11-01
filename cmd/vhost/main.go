package main

import (
	"flag"
	"fmt"
	protocol "iptcp-pedrocarlo/pkg/protocol"
	"iptcp-pedrocarlo/pkg/utils"
	"lnxconfig"
	"os"
)

func main() {
	var config = flag.String("config", "", "Configuration file")
	flag.Parse()
	if *config == "" {
		fmt.Println("No config file given")
		os.Exit(1)
		return
	}

	configIp, err := lnxconfig.ParseConfig(*config)
	if err != nil {
		panic(err)
	}
	host, err := protocol.Initialize(*configIp)
	if err != nil {
		panic(err)
	}
	if host.IsRouter {
		fmt.Println("Cannot pass a router lnx config to host binary")
		os.Exit(1)
	}


	utils.Repl(host)

}
