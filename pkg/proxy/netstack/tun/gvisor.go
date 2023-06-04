//go:build linux
// +build linux

package tun

import (
	"github.com/dismantl/gvisor/pkg/tcpip/link/fdbased"
	"github.com/dismantl/gvisor/pkg/tcpip/link/rawfile"
	"github.com/dismantl/gvisor/pkg/tcpip/link/tun"
	"github.com/dismantl/gvisor/pkg/tcpip/stack"
)

func Open(tunName string) (stack.LinkEndpoint, error) {
	mtu, err := rawfile.GetMTU(tunName)
	if err != nil {
		return nil, err
	}

	fd, err := tun.Open(tunName)
	if err != nil {
		return nil, err
	}

	linkEP, err := fdbased.New(&fdbased.Options{FDs: []int{fd}, MTU: mtu})
	if err != nil {
		return nil, err
	}
	return linkEP, nil
}
