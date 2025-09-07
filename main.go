package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/icmp"
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

func main() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/", func(c *gin.Context) { c.File("index.html") })

	r.GET("/api/ping", func(c *gin.Context) {
		input := strings.TrimSpace(c.Query("ip"))
		if input == "" {
			c.String(400, "missing ip")
			return
		}
		res := detectAndPing(c.Request.Context(), input)
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.String(200, "ipv4:%s,ipv6:%s", res.IPv4, res.IPv6)
	})

	r.GET("/api/ping/json", func(c *gin.Context) {
		input := strings.TrimSpace(c.Query("ip"))
		if input == "" {
			c.JSON(400, apiResponse{Code: 400, Msg: "missing ip"})
			return
		}
		res := detectAndPing(c.Request.Context(), input)
		c.JSON(200, apiResponse{Code: 200, Msg: "success", Data: res})
	})

	addr := ":5601"
	log.Printf("server listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}

// detectAndPing uses ICMP echo concurrently for v4/v6 with fast DNS and fallback
func detectAndPing(parent context.Context, input string) pingResult {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	res := pingResult{IPv4: "no", IPv6: "no"}
	parsed := net.ParseIP(input)

	var wg sync.WaitGroup
	if parsed != nil {
		if parsed.To4() != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if echoPing(ctx, parsed) || pingWithFamily(ctx, input, "4") {
					res.IPv4 = "ok"
				}
			}()
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if echoPing(ctx, parsed) || pingWithFamily(ctx, input, "6") {
					res.IPv6 = "ok"
				}
			}()
		}
		wg.Wait()
		return res
	}

	// Domain: resolve A/AAAA concurrently, then race ICMP and TCP (443/80)
	type addrList struct{ v []net.IP }
	var v4, v6 addrList
	wg.Add(2)
	go func() {
		defer wg.Done()
		if ips, _ := net.DefaultResolver.LookupIP(ctx, "ip4", input); len(ips) > 0 {
			v4.v = ips
		}
	}()
	go func() {
		defer wg.Done()
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
				res.IPv4 = "ok"
			}
		}()
	}
	if len(v6.v) > 0 {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			if raceEcho(ctx, v6.v) || tcpConnectRace(ctx, v6.v, "6", ports) || pingWithFamily(ctx, input, "6") {
				res.IPv6 = "ok"
			}
		}()
	}
	wg2.Wait()
	return res
}

// raceEcho pings multiple IPs concurrently and returns true if any succeeds
func raceEcho(ctx context.Context, ips []net.IP) bool {
	ctx2, cancel := context.WithTimeout(ctx, 2200*time.Millisecond)
	defer cancel()

	done := make(chan bool, 1)
	var once sync.Once
	for _, ip := range ips {
		ip := ip
		go func() {
			if echoPing(ctx2, ip) {
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

// echoPing sends a single ICMP echo request to given IP using raw sockets.
// Requires elevated privileges on some OS. If raw fails, returns false.
func echoPing(ctx context.Context, ip net.IP) bool {
	var network string
	var icmpType icmp.Type
	if ip.To4() != nil {
		network = "ip4:icmp"
		icmpType = ipv4.ICMPTypeEcho
	} else {
		network = "ip6:ipv6-icmp"
		icmpType = ipv6.ICMPTypeEchoRequest
	}

	c, err := icmp.ListenPacket(network, "0.0.0.0")
	if err != nil {
		return false
	}
	defer c.Close()

	msg := icmp.Message{
		Type: icmpType,
		Code: 0,
		Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: 1, Data: []byte("ping")},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return false
	}

	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)

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
	os := runtime.GOOS
	var cmd *exec.Cmd
	if os == "windows" {
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
