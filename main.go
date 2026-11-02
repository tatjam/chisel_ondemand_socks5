package main

import (
	"context"
	"fmt"
	"io"
	"log"
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

func (c *Chisel) setCancelLocked(cancel context.CancelFunc) {
	c.cancelFunc = cancel
	c.cancelFuncNumber++
}

func (c *Chisel) launchNewChiselLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	c.setCancelLocked(cancel)
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
	log.Printf("chisel is now running")
	err := cmd.Run()
	if err != nil && ctx.Err() == nil {
		log.Printf("chisel exited: %v", err)
	}

	log.Printf("chisel is now stopped")

	c.mu.Lock()
	if c.cancelFuncNumber == cancelNum {
		c.setCancelLocked(nil)
		if c.users > 0 {
			// Oh no! Run() exitted, and somebody was still using it
			c.launchNewChiselLocked()
		}
	}
	c.mu.Unlock()

}

func (c *Chisel) Acquire() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.users++
	if c.cancelFunc == nil {
		c.launchNewChiselLocked()
	}
}

func (c *Chisel) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.users--
	if c.users == 0 {
		// note the lastUserCh is buffered, so if we cannot send into
		// it, somebody already notified the "Release"! So just skip
		select {
		case c.lastUserCh <- struct{}{}:
		default:
		}
	}
}

func (c *Chisel) workerStop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.lastUserCh:
			log.Printf("no more users, maybe stopping...")
			// Maybe no more users, launch a little timer...
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			c.mu.Lock()
			if c.users == 0 && c.cancelFunc != nil {
				// Only if no new user came, we kill it
				c.cancelFunc()
				c.setCancelLocked(nil)
			}
			c.mu.Unlock()
		}
	}
}

func pipe(a net.Conn, b net.Conn) {
	// whichever direction finishes first closes the other end,
	// which instantly unblocks it. Thus at the end of this function,
	// both "threads" are dead
	go func() {
		io.Copy(a, b)
		a.Close()
	}()
	io.Copy(b, a)
	b.Close()
}

// Tries to initiate a connection with chisel with retry so if chisel
// is starting we don't fail instantly
func dialChisel(ctx context.Context, addr string) (net.Conn, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := new(net.Dialer).DialContext(ctx, "tcp", addr)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func serveRequest(ctx context.Context, chisel *Chisel, conn net.Conn) {
	chisel.Acquire()
	defer chisel.Release()

	toChisel, err := dialChisel(ctx, chisel.conf.localChiselHost)
	if err != nil {
		log.Printf("unable to open dialer to chisel: %v", err)
		return
	}

	// Note conn is closed by pipe
	pipe(toChisel, conn)
}

func main() {
	chiselUser := os.Getenv("CHISEL_USER")
	if len(chiselUser) == 0 {
		log.Fatal("must provide CHISEL_USER")
	}

	chiselPass := os.Getenv("CHISEL_PASS")
	if len(chiselPass) == 0 {
		log.Fatal("must provide CHISEL_PASS")
	}

	if len(os.Args) < 2 {
		log.Fatal("must provide chisel URL as argument")
	}

	localHost := os.Getenv("SOCKS_LOCAL_HOST")
	if len(localHost) == 0 {
		log.Println("using default address for SOCKS :1080")
		localHost = ":1080"
	}

	localChiselHost := os.Getenv("CHISEL_LOCAL_HOST")
	if len(localChiselHost) == 0 {
		log.Println("using default address for local chisel host localhost:1081")
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

	go chisel.workerStop(ctx)

	ln, err := net.Listen("tcp", localHost)
	if err != nil {
		log.Fatalf("unable to start listener at %s: %v", localHost, err)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("unable to handle connection", err)
			continue
		}

		go serveRequest(ctx, &chisel, conn)
	}
}
