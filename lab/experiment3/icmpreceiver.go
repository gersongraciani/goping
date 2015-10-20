package ggping

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type Ping struct {
	To          string
	Timeout     uint
	EchoMap     map[string]string
	When        time.Time
	Seq         int
	Pong        Pong
	EchoChannel chan *Ping

	pongchan chan *rawIcmp
}

type Pong struct {
	Rtt float64
	Err error
}

func runListener(handleRawIcmp func(ri *rawIcmp)) {
	//Creates the connection to send and receive packets
	c, err := net.ListenPacket("ip4:1", "0.0.0.0")
	if err != nil {
		log.Fatal("Could not open raw socket ip4:icmp: %v", err)
	}
	//defer c.Close()
	p := ipv4.NewPacketConn(c)
	if err := p.SetControlMessage(ipv4.FlagTTL|ipv4.FlagSrc|ipv4.FlagDst|ipv4.FlagInterface, true); err != nil {
		log.Fatal(err)
	}

	for {
		//Reads an ICMP Message from the Socket.
		ri := rawIcmp{bytes: make([]byte, 1500)}
		if ri.size, ri.cm, ri.peer, ri.err = p.ReadFrom(ri.bytes); ri.err != nil {
			log.Fatal("Could not read from socket: %v", ri.err)
		}

		//Tags the time when the message arrived. This will be used to calc RTT
		ri.when = time.Now()

		//Sends the Message to the checho channel
		go func(r rawIcmp) {
			handleRawIcmp(&r)
		}(ri)
	}
}
func coordinator(ping chan Ping, pongBuffer int) {

	//Maintains a sequence number
	var seq int

	//Creates the connection to send and receive packets
	c, err := net.ListenPacket("ip4:1", "0.0.0.0")
	if err != nil {
		log.Fatal("Could not open raw socket ip4:icmp: %v", err)
	}
	//defer c.Close()
	p := ipv4.NewPacketConn(c)
	if err := p.SetControlMessage(ipv4.FlagTTL|ipv4.FlagSrc|ipv4.FlagDst|ipv4.FlagInterface, true); err != nil {
		log.Fatal(err)
	}

	//Creates the handler to receive raw icmp
	pong := make(chan *rawIcmp, pongBuffer)
	var icmpRecvHandler = func(ri *rawIcmp) {
		pong <- ri
	}

	//Starts the icmp Listener in a goroutine
	go runListener(icmpRecvHandler)

	//Creates a map to match requests with a channel to send response
	var pingmap = make(map[int]chan *rawIcmp)

	for {
		select {
		case pi := <-ping:
			//Increment the sequence number and assigns to pi.Seq
			seq++
			pi.Seq = seq

			//Send the ping message. On error return the ping to EchoChannel if istantiated
			if err := sendMessage(&pi, p); err != nil {
				pi.Pong = Pong{Err: err}
				if pi.EchoChannel != nil {
					//Return the ping to the EchoChannel
					go func(pi *Ping) {
						pi.EchoChannel <- pi
					}(&pi)
				} else {
					log.Printf("Could not send ping %v [%v]\n", pi, err)
				}

				break //next select
			}

			//Initializes the channel to receive the Pong
			pi.pongchan = make(chan *rawIcmp, 2)

			//Registers the seq and the channel in the ping map
			pingmap[seq] = pi.pongchan

			go func(pi *Ping) {
				select {
				case ri := <-pi.pongchan:
					pi.Pong = Pong{Rtt: float64(pi.When.Sub(ri.when)) / float64(time.Millisecond)}
					//case <-time.After(time.Second * time.Duration(pi.Timeout)):
					//	pi.Pong = Pong{Err: fmt.Errorf("Request Timeout after %v seconds", pi.Timeout)}
				}
				if pi.EchoChannel != nil {
					pi.EchoChannel <- pi
				}
			}(&pi)

		case ri := <-pong:

			//Parsing the packet using golang icmp library
			rm, err := icmp.ParseMessage(1, ri.bytes[:ri.size])
			if err != nil {
				fmt.Printf("Could not parse message")
				break
			}

			//Testing for the type of icmp message
			if rm.Type != ipv4.ICMPTypeEchoReply {
				break
			}

			//Getting the ICMP Echo Reply
			body := rm.Body.(*icmp.Echo)
			if body.ID != os.Getpid() {
				fmt.Printf("Ignoring packet from external process")
				break
			}

			//Find the ping request in the map and send the packet through its channel
			if pingmap[seq] != nil {
				pingmap[seq] <- ri
				close(pingmap[seq])
				delete(pingmap, seq)
			}
		}
	}
}

type rawIcmp struct {
	when    time.Time
	size    int
	peer    net.Addr
	bytes   []byte
	cm      *ipv4.ControlMessage
	message *icmp.Echo //The message after being parsed
	err     error
}

func sendMessage(pi *Ping, p *ipv4.PacketConn) error {

	//Tries to convert the To attribute into an Ip attribute
	dst, err := net.ResolveIPAddr("ip4", pi.To)
	if err != nil {
		return fmt.Errorf("Could not resolve hostname: %v", pi.To)
	}

	//Creates the message to be sent based on Ping parameters
	wm := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Data: []byte("HELLO-R-U-THERE"),
		},
	}
	//Sets the Sequence of the Message
	wm.Body.(*icmp.Echo).Seq = pi.Seq

	//Serialize the message in a binary format
	wb, err := wm.Marshal(nil)
	if err != nil {
		return fmt.Errorf("Could not Marshall the icmp message")
	}

	//Writes the message into the socket
	pi.When = time.Now()
	if _, err := p.WriteTo(wb, nil, dst); err != nil {
		return fmt.Errorf("Could not send message through network")
	}
	return nil
}
