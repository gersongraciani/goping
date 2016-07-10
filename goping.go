package goping

import (
	"errors"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

/*** Structures ***/

//Config is the configures a GoPing object
type Config struct {
	Count      int
	Interval   time.Duration
	Timeout    time.Duration
	TOS        int
	TTL        int
	PacketSize int
}

//Request represents a Ping Job. A request can generate 1 to Count responses
type Request struct {
	Id       uint64
	Host     string
	Config   Config
	UserData map[string]string

	//Statistics
	Sent float64
}

//Response is sent for each Request Count iteration
type Response struct {
	Request Request
	Seq     int
	Err     error
	RawResponse
}

//RawResponse: Responses generated by the pinger implementation
type RawResponse struct {
	RTT         float64
	From        net.IP
	ICMPMessage icmp.Message
}

/*** Interfaces ***/

//Represent an logger object
type Logger interface {
	Warn(fmt string, v ...interface{})
	Info(fmt string, v ...interface{})
	Severe(fmt string, v ...interface{})
	IsDebug() bool
	Debug(fmt string, v ...interface{})
}

//Pinger is responsible for send and receive pings over the network
type Pinger interface {
	Ping(r Request) (future <-chan RawResponse, seq int, err error)
}

//GoPing Coordinates ping requests and responses
type Gopinger interface {
	NewRequest(hostname string, userData map[string]string) Request
	Start() (chan<- Request, <-chan Response)
}

/*** Interface Implementation ***/
type goping struct {
	idGen  uint64
	cfg    Config
	log    Logger
	pinger Pinger
}

func (g *goping) NewRequest(hostname string, userData map[string]string) Request {
	id := atomic.AddUint64(&(g.idGen), 1)
	return Request{
		Id:       id,
		Host:     hostname,
		Config:   g.cfg,
		UserData: userData,
	}
}

func (g *goping) Start() (chan<- Request, <-chan Response) {
	in := make(chan Request)
	pin := make(chan Request)
	out := make(chan Response)
	doneIn := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup

	go func(in chan Request, out chan Response) {
		for {
			select {
			case recv, open := <-in:
				if !open {
					//Stop reading from channel
					in = nil
					go func() {
						//Send signal to doneIn because channel is closed
						doneIn <- struct{}{}
					}()
				} else {
					wg.Add(1)
					if recv.Config.Count == 0 {
						//Request  Count is 0. Job is done without sending any requests
						wg.Done()
					} else {
						//Send request to be processed
						go func() {
							pin <- recv
						}()
					}
				}

			case recv := <-pin:
				//Incrementing Request Sent Counter
				recv.Sent++

				//Calling Ping method of the pinger interface
				future, seq, err := g.pinger.Ping(recv)

				//waiting for a response in a goroutine
				go func(recv Request, future <-chan RawResponse, seq int, err error) {

					//Builds the response object
					resp := Response{
						Request:     recv,
						Seq:         seq,
						Err:         err,
						RawResponse: RawResponse{RTT: math.NaN()},
					}

					//Start a timer to the request interval
					waitInterval := time.After(recv.Config.Interval)

					if resp.Err == nil {
						timeout := time.After(recv.Config.Timeout)
						select {
						case <-timeout:
							resp.Err = errors.New("Timeout")
						case r := <-future:
							resp.RawResponse = r
							switch r.ICMPMessage.Type {
							case ipv4.ICMPTypeEcho:
							case ipv4.ICMPTypeEchoReply:
							case ipv4.ICMPTypeDestinationUnreachable:
								resp.Err = errors.New("Destination Unreachable")
							case ipv4.ICMPTypeTimeExceeded:
								resp.Err = errors.New("Time Exceeded")
							case ipv4.ICMPTypeParameterProblem:
								resp.Err = errors.New("Parameter Problem")
							case ipv4.ICMPTypeRedirect:
								resp.Err = errors.New("Redirect")
							default:
								//TODO: Recognize all possible ICMP TYpes
								resp.Err = errors.New("uncategorized ICMP RAW Response Type")
							}
						}
					}

					//Send response to out channel. Blocks until user consumes it
					out <- resp

					if recv.Config.Count >= 0 && int(recv.Sent) >= recv.Config.Count {
						//This was the last request. Job Done
						wg.Done()
					} else {
						//We still have more requests to do. Waits for the request interval and send request to pin channel again
						<-waitInterval
						pin <- recv
					}
				}(recv, future, seq, err)

			case <-doneIn:
				go func(wg *sync.WaitGroup, out chan Response, done chan struct{}) {
					wg.Wait()
					close(out)
					done <- struct{}{}
				}(&wg, out, done)

			case <-done:
				return
			}
		}

	}(in, out)

	return in, out
}

/*** Constructors ***/
func New(cfg Config, log Logger, pinger Pinger) Gopinger {
	return &goping{
		cfg:    cfg,
		log:    log,
		pinger: pinger,
	}
}