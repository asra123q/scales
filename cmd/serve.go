/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// Enum for health
type Health int

const (
	Unknown Health = iota
	Alive
	Dead
)

func (h Health) String() string {
	switch h {
	case Alive:
		return "alive"
	case Dead:
		return "dead"
	default:
		return "unknown"
	}
}

// Server type struct
type Server struct {
	ID                uint
	URL               *url.URL
	Health            Health
	ActiveConnections int

	proxy *httputil.ReverseProxy
}

var (
	poolMu     sync.RWMutex
	serverPool = make(map[uint]*Server)
)

func AddServer(id uint, host string, port uint16) (*Server, error) {
	u, err := url.Parse("http://" + net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, fmt.Errorf("server %d: parse url: %w", id, err)
	}

	s := &Server{ID: id, URL: u, Health: Unknown, proxy: httputil.NewSingleHostReverseProxy(u)}

	poolMu.Lock()
	serverPool[id] = s
	poolMu.Unlock()

	return s, nil
}

func HealthCheck() {
	poolMu.RLock()
	servers := make([]*Server, 0, len(serverPool))
	for _, s := range serverPool {
		servers = append(servers, s)
	}
	poolMu.RUnlock()

	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func(s *Server) {
			defer wg.Done()
			checkServer(s)
		}(s)
	}
	wg.Wait()
}

func checkServer(s *Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL.String(), nil)
	if err != nil {
		fmt.Printf("error creating request: %v for server: %v\n", err, s.ID)
		SetHealth(s, Dead)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("request failed or timeout: %v for server: %v\n", err, s.ID)
		SetHealth(s, Dead)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("request failed with status: %v for server: %v\n", resp.Status, s.ID)
		SetHealth(s, Dead)
		return
	}

	fmt.Printf("server: %v reached successfully\n", s.ID)
	SetHealth(s, Alive)
}

func SetHealth(s *Server, h Health) {
	poolMu.Lock()
	defer poolMu.Unlock()
	s.Health = h
}

func GetServers() []Server {
	poolMu.RLock()
	defer poolMu.RUnlock()
	out := make([]Server, 0, len(serverPool))
	for _, s := range serverPool {
		out = append(out, Server{
			ID:                s.ID,
			URL:               s.URL,
			Health:            s.Health,
			ActiveConnections: s.ActiveConnections,
		})
	}
	return out
}

func DeleteServer(id uint) {
	poolMu.Lock()
	defer poolMu.Unlock()
	delete(serverPool, id)
}

// Enum for strategies
type Strategy int

const (
	RoundRobin Strategy = iota
	LeastConnections
)

func (s Strategy) String() string {
	switch s {
	case RoundRobin:
		return "RoundRobin"
	case LeastConnections:
		return "LeastConnections"
	default:
		return "RoundRobin"
	}
}

// LoadBalancer type struct
type LoadBalancer struct {
	Strategy        Strategy
	Servers         map[uint]*Server
	Port            string
	RoundRobinCount int

	mu sync.Mutex
}

func NewLoadBalancer(port string, strategy Strategy) *LoadBalancer {
	return &LoadBalancer{
		Strategy: strategy,
		Servers:  serverPool,
		Port:     port,
	}
}

func GetHealthyServers() []Server {
	var healthyServers []Server
	for _, s := range GetServers() {
		switch s.Health {
		case Alive:
			healthyServers = append(healthyServers, s)
		}
	}
	return healthyServers
}

// getHealthyServerPtrs returns pointers into the live server pool, sorted
// by ID, so that selection strategies observe and can act on the real
// ActiveConnections state rather than a snapshot copy. The sort gives
// RoundRobin a stable ordering to cycle through — serverPool is a map, so
// without it the order (and therefore which server "index N" means) would
// change randomly between calls, skewing the rotation.
func getHealthyServerPtrs() []*Server {
	poolMu.RLock()
	defer poolMu.RUnlock()
	var healthy []*Server
	for _, s := range serverPool {
		if s.Health == Alive {
			healthy = append(healthy, s)
		}
	}
	sort.Slice(healthy, func(i, j int) bool { return healthy[i].ID < healthy[j].ID })
	return healthy
}

// leastConnectionsServer picks the healthy server with the fewest active
// connections. It holds poolMu for the whole scan so the comparison can't
// race with proxyHandler's increment/decrement of ActiveConnections.
func leastConnectionsServer() (*Server, error) {
	poolMu.RLock()
	defer poolMu.RUnlock()

	var least *Server
	for _, s := range serverPool {
		if s.Health != Alive {
			continue
		}
		if least == nil || s.ActiveConnections < least.ActiveConnections {
			least = s
		}
	}
	if least == nil {
		return nil, fmt.Errorf("there are no healthy servers")
	}
	return least, nil
}

func (lb *LoadBalancer) GetNextAvailableServer() (*Server, error) {
	switch lb.Strategy {

	case RoundRobin:
		servers := getHealthyServerPtrs()
		if len(servers) == 0 {
			return nil, fmt.Errorf("there are no healthy servers")
		}

		lb.mu.Lock()
		next := servers[lb.RoundRobinCount%len(servers)]
		lb.RoundRobinCount = (lb.RoundRobinCount + 1) % len(servers)
		lb.mu.Unlock()

		return next, nil

	case LeastConnections:
		return leastConnectionsServer()
	}

	return nil, fmt.Errorf("unknown load balancing strategy: %v", lb.Strategy)
}

// ServeHTTP picks a backend via the configured strategy and proxies the
// request to it, tracking ActiveConnections around the proxied call so
// LeastConnections has live data to compare against.
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	server, err := lb.GetNextAvailableServer()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	poolMu.Lock()
	server.ActiveConnections++
	poolMu.Unlock()
	defer func() {
		poolMu.Lock()
		server.ActiveConnections--
		poolMu.Unlock()
	}()

	server.proxy.ServeHTTP(w, r)
}

func parseStrategy(s string) (Strategy, error) {
	switch s {
	case "round-robin", "":
		return RoundRobin, nil
	case "least-connections":
		return LeastConnections, nil
	default:
		return 0, fmt.Errorf("unknown strategy %q (want %q or %q)", s, "round-robin", "least-connections")
	}
}

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the load balancer",
	Long:  `Starts an HTTP load balancer that health-checks and distributes requests across a set of backend servers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetString("port")
		backends, _ := cmd.Flags().GetStringSlice("backends")
		strategyFlag, _ := cmd.Flags().GetString("strategy")
		healthInterval, _ := cmd.Flags().GetDuration("health-interval")

		strategy, err := parseStrategy(strategyFlag)
		if err != nil {
			return err
		}

		for i, backend := range backends {
			host, portStr, err := net.SplitHostPort(backend)
			if err != nil {
				return fmt.Errorf("invalid backend %q: %w", backend, err)
			}
			p, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return fmt.Errorf("invalid backend port %q: %w", backend, err)
			}
			if _, err := AddServer(uint(i+1), host, uint16(p)); err != nil {
				return fmt.Errorf("add backend %q: %w", backend, err)
			}
		}

		// Run an initial health check synchronously so the pool isn't
		// empty the moment we start accepting requests.
		HealthCheck()

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		go func() {
			ticker := time.NewTicker(healthInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					HealthCheck()
				}
			}
		}()

		lb := NewLoadBalancer(port, strategy)
		httpServer := &http.Server{
			Addr:    ":" + port,
			Handler: lb,
		}

		serveErr := make(chan error, 1)
		go func() {
			fmt.Printf("load balancer (%s) listening on :%s\n", strategy, port)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				serveErr <- err
				return
			}
			serveErr <- nil
		}()

		select {
		case <-ctx.Done():
		case err := <-serveErr:
			if err != nil {
				return err
			}
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().StringP("port", "p", "3000", "port for the load balancer to listen on")
	serveCmd.Flags().StringSliceP("backends", "b", []string{"localhost:8081", "localhost:8082", "localhost:8083"}, "backend servers as host:port")
	serveCmd.Flags().StringP("strategy", "s", "round-robin", "load balancing strategy: round-robin or least-connections")
	serveCmd.Flags().Duration("health-interval", 10*time.Second, "interval between backend health checks")
}
