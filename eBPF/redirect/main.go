package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/cilium/ebpf"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror" redirect ./ebpf/redirect.c -- -I../../headers

func main() {

	ifaceName := flag.String("interface", "lo", "The interface to watch network traffic on")

	flag.Parse()

	log.Infof("Starting 🐝 the eBPF redirecter, on interface [%s]", *ifaceName)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Look up the network interface by name.
	devID, err := net.InterfaceByName(*ifaceName)
	if err != nil {
		panic(fmt.Sprintf("lookup network iface %s: %s", *ifaceName, err))
	}

	objs := redirectObjects{}

	if err := loadRedirectObjects(&objs, nil); err != nil {
		var verr *ebpf.VerifierError
		if errors.As(err, &verr) {
			fmt.Printf("%+v\n", verr)
		}
		log.Fatalf("loading objects: %s", err)
	}
	defer objs.Close()

	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: devID.Index,
			Handle:    netlink.MakeHandle(0xffff, 0),
			Parent:    netlink.HANDLE_INGRESS,
		},
		QdiscType: "clsact",
	}

	err = netlink.QdiscReplace(qdisc)
	if err != nil {
		log.Fatalf("could not get replace qdisc: %v", err)
	}
	log.Info("Loaded TC QDisc")

	filterIngress := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: devID.Index,
			Parent:    netlink.HANDLE_MIN_INGRESS,
			Handle:    1,
			Protocol:  unix.ETH_P_ALL,
		},
		Fd:           objs.TcIngress.FD(),
		Name:         objs.TcIngress.String(),
		DirectAction: true,
	}

	if err := netlink.FilterReplace(filterIngress); err != nil {
		log.Fatalf("failed to replace tc filter: %v", err)
	}

	filterEgress := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: devID.Index,
			Parent:    netlink.HANDLE_MIN_EGRESS,
			Handle:    1,
			Protocol:  unix.ETH_P_ALL,
		},
		Fd:           objs.TcEgress.FD(),
		Name:         objs.TcEgress.String(),
		DirectAction: true,
	}

	if err := netlink.FilterReplace(filterEgress); err != nil {
		log.Fatalf("failed to replace tc filter: %v", err)
	}

	log.Printf("Press Ctrl-C to exit and remove the program")

	// Drop the logs
	go cat()
	<-ctx.Done() // We wait here

	log.Info("Removing eBPF programs")

	link, err := netlink.LinkByName(*ifaceName)
	if err != nil {
		log.Fatalf("could not find iface: %v", err)
	}

	f, err := netlink.FilterList(link, netlink.HANDLE_MIN_INGRESS)
	if err != nil {
		log.Fatalf("could not list filters: %v", err)
	}

	if len(f) == 0 {
		log.Error("Unable to clean any filters")
	}
	for x := range f {
		err = netlink.FilterDel(f[x])
		if err != nil {
			log.Fatalf("could not get remove filter: %v", err)
		}
	}

	f, err = netlink.FilterList(link, netlink.HANDLE_MIN_EGRESS)
	if err != nil {
		log.Fatalf("could not list filters: %v", err)
	}

	if len(f) == 0 {
		log.Error("Unable to clean any filters")
	}
	for x := range f {
		err = netlink.FilterDel(f[x])
		if err != nil {
			log.Fatalf("could not get remove filter: %v", err)
		}
	}
}
func readLines(r io.Reader) {
	rd := bufio.NewReader(r)
	for {
		line, err := rd.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("%s", line)

	}
}

func cat() {
	file, err := os.Open("/sys/kernel/tracing/trace_pipe")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	readLines(file)
}
