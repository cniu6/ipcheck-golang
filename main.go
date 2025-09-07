package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/icmp"
	"golang.org/x/net/idna"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// apiResponse is the JSON response structure for /api/ping/json
type apiResponse struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

// pingResult holds IPv4/IPv6 results
type pingResult struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

// Global semaphores to cap concurrent operations (configurable via env)
var (
	semDNS  chan struct{}
	semICMP chan struct{}
	semTCP  chan struct{}
)

func init() {
	semDNS = make(chan struct{}, getEnvInt("MAX_DNS", 4096))
	semICMP = make(chan struct{}, getEnvInt("MAX_ICMP", 8192))
	semTCP = make(chan struct{}, getEnvInt("MAX_TCP", 8192))
}

func getEnvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil || i <= 0 {
		return def
	}
	return i
}

func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func release(sem chan struct{}) { <-sem }

func main() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// Security headers (CSP allows inline style/script for this single-page app)
	r.Use(func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		// Allow inline style/script and same-origin fetch/img
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		c.Next()
	})

	r.GET("/", func(c *gin.Context) { c.File("index.html") })

	r.GET("/api/ping", func(c *gin.Context) {
		input := strings.TrimSpace(c.Query("ip"))
		if !isValidInput(input) {
			c.String(400, "invalid ip or domain")
			return
		}
		res := detectAndPing(c.Request.Context(), input)
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.String(200, "ipv4:%s,ipv6:%s", res.IPv4, res.IPv6)
	})

	r.GET("/api/ping/json", func(c *gin.Context) {
		input := strings.TrimSpace(c.Query("ip"))
		if !isValidInput(input) {
			c.JSON(400, apiResponse{Code: 400, Msg: "invalid ip or domain"})
			return
		}
		res := detectAndPing(c.Request.Context(), input)
		c.JSON(200, apiResponse{Code: 200, Msg: "success", Data: res})
	})

	addr := ":5601"
	log.Printf("server listening on %s", addr)
	// Custom server with timeouts to prevent slowloris
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      7 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// isValidInput validates IPv4/IPv6/Domain and normalizes domain using IDNA
func isValidInput(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	if ip := net.ParseIP(s); ip != nil {
		return true
	}
	// domain: only letters/digits/hyphen/dot and punycode after idna
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil || ascii == "" || len(ascii) > 253 {
		return false
	}
	// simple domain regex
	var reDomain = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*)$`)
	return reDomain.MatchString(ascii)
}

// detectAndPing uses ICMP echo concurrently for v4/v6 with fast DNS and TCP fallback
func detectAndPing(parent context.Context, input string) pingResult {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	res := pingResult{IPv4: "no", IPv6: "no"}
	parsed := net.ParseIP(input)

	var wg sync.WaitGroup
	var v4ok, v6ok int32 // atomic flags

	setV4 := func() { atomic.StoreInt32(&v4ok, 1) }
	setV6 := func() { atomic.StoreInt32(&v6ok, 1) }

	if parsed != nil {
		if parsed.To4() != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if doICMP(ctx, parsed) || pingWithFamily(ctx, input, "4") {
					setV4()
				}
			}()
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if doICMP(ctx, parsed) || pingWithFamily(ctx, input, "6") {
					setV6()
				}
			}()
		}
		wg.Wait()
		if atomic.LoadInt32(&v4ok) == 1 {
			res.IPv4 = "ok"
		}
		if atomic.LoadInt32(&v6ok) == 1 {
			res.IPv6 = "ok"
		}
		return res
	}

	// Domain: resolve A/AAAA concurrently (with semaphore), then race ICMP and TCP (443/80)
	type addrList struct{ v []net.IP }
	var v4, v6 addrList
	wg.Add(2)
	go func() {
		defer wg.Done()
		if !acquire(ctx, semDNS) {
			return
		}
		defer release(semDNS)
		if ips, _ := net.DefaultResolver.LookupIP(ctx, "ip4", input); len(ips) > 0 {
			v4.v = ips
		}
	}()
	go func() {
		defer wg.Done()
		if !acquire(ctx, semDNS) {
			return
		}
		defer release(semDNS)
		if ips, _ := net.DefaultResolver.LookupIP(ctx, "ip6", input); len(ips) > 0 {
			v6.v = ips
		}
	}()
	wg.Wait()

	ports := []string{"443", "80"}
	var wg2 sync.WaitGroup
	if len(v4.v) > 0 {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if raceEcho(ctx, v4.v) || tcpConnectRace(ctx, v4.v, "4", ports) || pingWithFamily(ctx, input, "4") {
				setV4()
			}
		}()
	}
	if len(v6.v) > 0 {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if raceEcho(ctx, v6.v) || tcpConnectRace(ctx, v6.v, "6", ports) || pingWithFamily(ctx, input, "6") {
				setV6()
			}
		}()
	}
	wg2.Wait()
	if atomic.LoadInt32(&v4ok) == 1 {
		res.IPv4 = "ok"
	}
	if atomic.LoadInt32(&v6ok) == 1 {
		res.IPv6 = "ok"
	}
	return res
}

// raceEcho pings multiple IPs concurrently and returns true if any succeeds (with semaphore)
func raceEcho(ctx context.Context, ips []net.IP) bool {
	ctx2, cancel := context.WithTimeout(ctx, 2200*time.Millisecond)
	defer cancel()

	done := make(chan bool, 1)
	var once sync.Once
	for _, ip := range ips {
		ip := ip
		go func() {
			if !acquire(ctx2, semICMP) {
				return
			}
			defer release(semICMP)
			if doICMP(ctx2, ip) {
				once.Do(func() { done <- true })
			}
		}()
	}
	select {
	case <-done:
		return true
	case <-ctx2.Done():
		return false
	}
}

// tcpConnectRace tries connecting to the target IPs on given ports (any success => true)
func tcpConnectRace(ctx context.Context, ips []net.IP, family string, ports []string) bool {
	ctx2, cancel := context.WithTimeout(ctx, 2200*time.Millisecond)
	defer cancel()
	done := make(chan bool, 1)
	var once sync.Once

	dialNet := "tcp4"
	if family == "6" {
		dialNet = "tcp6"
	}

	for _, ip := range ips {
		ip := ip
		for _, p := range ports {
			p := p
			go func() {
				if !acquire(ctx2, semTCP) {
					return
				}
				defer release(semTCP)
				d := net.Dialer{Timeout: 1200 * time.Millisecond}
				conn, err := d.DialContext(ctx2, dialNet, net.JoinHostPort(ip.String(), p))
				if err == nil {
					_ = conn.Close()
					once.Do(func() { done <- true })
				}
			}()
		}
	}

	select {
	case <-done:
		return true
	case <-ctx2.Done():
		return false
	}
}

// doICMP sends a single ICMP echo request to given IP using raw sockets. Returns false if not permitted.
func doICMP(ctx context.Context, ip net.IP) bool {
	var network, laddr string
	var icmpType icmp.Type
	if ip.To4() != nil {
		network = "ip4:icmp"
		laddr = "0.0.0.0"
		icmpType = ipv4.ICMPTypeEcho
	} else {
		network = "ip6:ipv6-icmp"
		laddr = "::"
		icmpType = ipv6.ICMPTypeEchoRequest
	}

	c, err := icmp.ListenPacket(network, laddr)
	if err != nil {
		return false
	}
	defer c.Close()

	msg := icmp.Message{Type: icmpType, Code: 0, Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: 1, Data: []byte("ping")}}
	b, err := msg.Marshal(nil)
	if err != nil {
		return false
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(deadline)
	}

	if _, err = c.WriteTo(b, &net.IPAddr{IP: ip}); err != nil {
		return false
	}

	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return false
		default:
			n, _, err := c.ReadFrom(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					return false
				}
				if errors.Is(err, os.ErrDeadlineExceeded) {
					return false
				}
				return false
			}
			rm, err := icmp.ParseMessage(getProto(ip), buf[:n])
			if err == nil && (rm.Type == ipv4.ICMPTypeEchoReply || rm.Type == ipv6.ICMPTypeEchoReply) {
				return true
			}
		}
	}
}

func getProto(ip net.IP) int {
	if ip.To4() != nil {
		return 1
	}
	return 58
}

// pingWithFamily executes the system ping command for IPv4(-4) or IPv6(-6) as fallback.
func pingWithFamily(ctx context.Context, host string, family string) bool {
	osn := runtime.GOOS
	var cmd *exec.Cmd
	if osn == "windows" {
		args := []string{"-n", "1", "-w", "1500"}
		if family == "4" {
			args = append([]string{"-4"}, args...)
		} else {
			args = append([]string{"-6"}, args...)
		}
		args = append(args, host)
		cmd = exec.CommandContext(ctx, "ping", args...)
	} else {
		args := []string{"-c", "1"}
		if family == "4" {
			args = append([]string{"-4"}, args...)
		} else {
			args = append([]string{"-6"}, args...)
		}
		args = append(args, "-W", "1", host)
		cmd = exec.CommandContext(ctx, "ping", args...)
	}
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}
