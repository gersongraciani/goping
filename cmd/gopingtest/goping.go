package main

import (
	"fmt"
	"time"

	"github.com/gracig/goping"
	"github.com/gracig/goping/pingers/linuxICMPv4"
)

func main() {
	cfg := goping.Config{
		Count:      -1,
		Interval:   time.Duration(1 * time.Second),
		PacketSize: 100,
		TOS:        0,
		TTL:        64,
		Timeout:    time.Duration(3 * time.Second),
	}
	p := goping.New(cfg, linuxICMPv4.New(), nil, nil)
	ping, pong, err := p.Start()
	if err != nil {
		fmt.Println("Could not start ping")
	}
	go func() {
		for i := 0; i < 1; i++ {
			ping <- p.NewRequest("8.8.8.8", nil)
		}
		close(ping)
	}()
	for resp := range pong {
		fmt.Printf("Received response %v\n", resp)
	}
}
