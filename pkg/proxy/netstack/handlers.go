package netstack

import (
	"io"

	"github.com/dismantl/gvisor/pkg/tcpip/adapters/gonet"
	"github.com/dismantl/gvisor/pkg/tcpip/header"
	"github.com/dismantl/gvisor/pkg/tcpip/stack"
	"github.com/dismantl/gvisor/pkg/tcpip/transport/icmp"
	"github.com/dismantl/gvisor/pkg/tcpip/transport/tcp"
	"github.com/dismantl/gvisor/pkg/tcpip/transport/udp"
	"github.com/dismantl/gvisor/pkg/waiter"
	"github.com/dismantl/ligolo-ng/pkg/protocol"
	"github.com/dismantl/ligolo-ng/pkg/relay"
	"github.com/hashicorp/yamux"
	"github.com/sirupsen/logrus"
)

// handleICMP process incoming ICMP packets and, depending on the target host status, respond a ICMP ECHO Reply
// Please note that other ICMP messages are not yet supported.
func handleICMP(nstack *stack.Stack, localConn TunConn, yamuxConn *yamux.Session) {
	pkt := localConn.GetICMP().Request
	v, ok := pkt.Data().PullUp(header.ICMPv4MinimumSize)
	if !ok {
		return
	}
	h := header.ICMPv4(v)
	if h.Type() == header.ICMPv4Echo {
		iph := header.IPv4(pkt.NetworkHeader().Slice())
		yamuxConnectionSession, err := yamuxConn.Open()
		if err != nil {
			logrus.Error(err)
			return
		}
		logrus.Debugf("Checking if %s is alive...\n", iph.DestinationAddress().String())
		icmpPacket := protocol.HostPingRequestPacket{Address: iph.DestinationAddress().String()}

		protocolEncoder := protocol.NewEncoder(yamuxConnectionSession)
		protocolDecoder := protocol.NewDecoder(yamuxConnectionSession)

		if err := protocolEncoder.Encode(protocol.Envelope{
			Type:    protocol.MessageHostPingRequest,
			Payload: icmpPacket,
		}); err != nil {
			logrus.Error(err)
			return
		}

		logrus.Debug("Awaiting ping response...")
		if err := protocolDecoder.Decode(); err != nil {
			logrus.Error(err)
			return
		}

		response := protocolDecoder.Envelope.Payload
		reply := response.(protocol.HostPingResponsePacket)
		if reply.Alive {
			logrus.Debug("Host is alive, sending reply")
			ProcessICMP(nstack, pkt)

		}

	}
	// Ignore other ICMPs
	return
}

func HandlePacket(nstack *stack.Stack, localConn TunConn, yamuxConn *yamux.Session) {

	var endpointID stack.TransportEndpointID
	var prototransport uint8
	var protonet uint8

	// Switching part
	switch localConn.Protocol {
	case tcp.ProtocolNumber:
		endpointID = localConn.GetTCP().EndpointID
		prototransport = protocol.TransportTCP
	case udp.ProtocolNumber:
		endpointID = localConn.GetUDP().EndpointID
		prototransport = protocol.TransportUDP
	case icmp.ProtocolNumber4:
		// ICMPs can't be relayed
		handleICMP(nstack, localConn, yamuxConn)
		return
	}

	if endpointID.LocalAddress.To4().String() != "" {
		protonet = protocol.Networkv4
	} else {
		protonet = protocol.Networkv6
	}

	logrus.Debugf("Got packet source : %s - endpointID : %s:%d", endpointID.RemoteAddress, endpointID.LocalAddress, endpointID.LocalPort)

	yamuxConnectionSession, err := yamuxConn.Open()
	if err != nil {
		logrus.Error(err)
		return
	}
	connectPacket := protocol.ConnectRequestPacket{
		Net:       protonet,
		Transport: prototransport,
		Address:   endpointID.LocalAddress.String(),
		Port:      endpointID.LocalPort,
	}

	protocolEncoder := protocol.NewEncoder(yamuxConnectionSession)
	protocolDecoder := protocol.NewDecoder(yamuxConnectionSession)

	if err := protocolEncoder.Encode(protocol.Envelope{
		Type:    protocol.MessageConnectRequest,
		Payload: connectPacket,
	}); err != nil {
		logrus.Error(err)
		return
	}

	logrus.Debug("Awaiting response...")
	if err := protocolDecoder.Decode(); err != nil {
		if err != io.EOF {
			logrus.Error(err)
		}
		return
	}

	response := protocolDecoder.Envelope.Payload
	reply := response.(protocol.ConnectResponsePacket)
	if reply.Established {
		logrus.Debug("Connection established on remote end!")
		go func() {
			var wq waiter.Queue
			if localConn.IsTCP() {
				ep, iperr := localConn.GetTCP().Request.CreateEndpoint(&wq)
				if iperr != nil {
					logrus.Error(iperr)
					localConn.Terminate(true)
					return
				}
				gonetConn := gonet.NewTCPConn(&wq, ep)
				go relay.StartRelay(yamuxConnectionSession, gonetConn)

			} else if localConn.IsUDP() {
				ep, iperr := localConn.GetUDP().Request.CreateEndpoint(&wq)
				if iperr != nil {
					logrus.Error(iperr)
					localConn.Terminate(false)
					return
				}

				gonetConn := gonet.NewUDPConn(nstack, &wq, ep)
				go relay.StartRelay(yamuxConnectionSession, gonetConn)
			}

		}()
	} else {
		localConn.Terminate(reply.Reset)

	}

}
