// Command egressclient runs inside a sandbox container in the egress
// integration test. Each argument pair is a probe; one result line per
// probe goes to the file named by the first argument.
//
//	get <url>        OK <status> <body> | ERR <error>
//	dial <host:port> OPEN | BLOCKED
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	out, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer out.Close()
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	args := os.Args[2:]
	for i := 0; i+1 < len(args); i += 2 {
		verb, target := args[i], args[i+1]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		switch verb {
		case "get":
			fmt.Fprintln(out, get(ctx, c, target))
		case "dial":
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", target)
			if err != nil {
				fmt.Fprintln(out, "BLOCKED")
			} else {
				_ = conn.Close()
				fmt.Fprintln(out, "OPEN")
			}
		}
		cancel()
	}
}

func get(ctx context.Context, c *http.Client, target string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "ERR " + err.Error()
	}
	resp, err := c.Do(req)
	if err != nil {
		return "ERR " + strings.ReplaceAll(err.Error(), "\n", " ")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return fmt.Sprintf("OK %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
