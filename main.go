package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

type ChiselConfig struct {
	user string
	pass string
	// Remote chisel connection URL
	url string
	// Local SOCKS exposed by chisel
	localChiselHost string
}

type Chisel struct {
	conf  ChiselConfig
	mu    sync.Mutex
	users int
	// This being non-nil indicates chisel is running
	cancelFunc context.CancelFunc
	// To be able to determine when cancelFunc is to be cleared
	cancelFuncNumber uint
	lastUserCh       chan struct{}
}

// Call while mu is held
func (c *Chisel) setCancel(cancel context.CancelFunc) {
	c.cancelFunc = cancel
	c.cancelFuncNumber++
}

// Only call with mutex locked
func (c *Chisel) launchNewChisel() {
	ctx, cancel := context.WithCancel(context.Background())
	c.setCancel(cancel)
	cancelNum := c.cancelFuncNumber
	go c.chiselWorker(ctx, cancelNum)
}

func (c *Chisel) chiselWorker(ctx context.Context, cancelNum uint) {
	cmd := exec.CommandContext(ctx, "chisel",
		"client",
		"--auth", fmt.Sprintf("%s:%s", c.conf.user, c.conf.pass),
		c.conf.url,
		fmt.Sprintf("%s:socks", c.conf.localChiselHost),
	)
	cmd.Run()

	c.mu.Lock()
	if c.cancelFuncNumber == cancelNum {
		c.setCancel(nil)
		if c.users > 0 {
			// Oh no! Run() exitted, and somebody tried to
			// connect again while it was dead, but cancelFunc was
			// still set, thus no launchChisel was called, call it ourselves
			c.launchNewChisel()
		}
	}
	c.mu.Unlock()

}

func (c *Chisel) Ensure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.users++
	if c.cancelFunc == nil {
		c.launchNewChisel()
	}
}

func (c *Chisel) Done() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.users--
	if c.users == 0 {
		// note the lastUserCh is buffered, so if we cannot send into
		// it, somebody already notified the "Done"! So just skip
		select {
		case c.lastUserCh <- struct{}{}:
		default:
		}
	}
}

func serveStop(ctx context.Context, chisel *Chisel) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-chisel.lastUserCh:
			// Maybe no more users, launch a little timer...
			time.Sleep(30 * time.Second)
			chisel.mu.Lock()
			if chisel.users == 0 {
				// Only if no new user came, we kill it
				chisel.cancelFunc()
				chisel.setCancel(nil)
			}
			chisel.mu.Unlock()
		}
	}
}

func serveRequest(ctx context.Context, chisel *Chisel, conn net.Conn) {
	chisel.Ensure()
	defer chisel.Done()
}

func main() {
	chiselUser := os.Getenv("CHISEL_USER")
	if len(chiselUser) == 0 {
		println("Must provide CHISEL_USER")
		os.Exit(1)
	}

	chiselPass := os.Getenv("CHISEL_PASS")
	if len(chiselPass) == 0 {
		println("Must provide CHISEL_PASS")
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		println("Must provide chisel URL as argument")
		os.Exit(1)
	}

	localHost := os.Getenv("SOCKS_LOCAL_HOST")
	if len(localHost) == 0 {
		println("Using default address for SOCKS :1080")
		localHost = ":1080"
	}

	localChiselHost := os.Getenv("CHISEL_LOCAL_HOST")
	if len(localChiselHost) == 0 {
		println("Using default address for local chisel host localhost:1081")
		localChiselHost = "localhost:1081"
	}

	config := ChiselConfig{
		user:            chiselUser,
		pass:            chiselPass,
		url:             os.Args[1],
		localChiselHost: localChiselHost,
	}

	chisel := Chisel{
		conf:             config,
		mu:               sync.Mutex{},
		users:            0,
		cancelFuncNumber: 0,
		lastUserCh:       make(chan struct{}, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go serveStop(ctx, &chisel)

	ln, err := net.Listen("tcp", localHost)
	if err != nil {
		println("Unable to start listener at", localHost)
		os.Exit(1)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			println("Unable to handle connection", err)
			continue
		}

		go serveRequest(ctx, &chisel, conn)
	}
}
