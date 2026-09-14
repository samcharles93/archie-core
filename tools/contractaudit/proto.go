package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// protoSurface reports RPC coverage for one gRPC service.
//
// Consumption is established by scanning the adapter package for calls through
// the generated client, e.g. `c.client.ListSessions(...)`. The method-name
// lookup an earlier version used was wrong in a way worth recording: the
// adapters do not rename RPCs one-to-one. `StoreClient.Get` calls GetSession,
// `StoreClient.List` calls ListSessions, `StoreClient.GetByChannel` calls
// GetSessionsByChannel -- so a name-based join silently reports live RPCs as
// unconsumed, which is exactly the false finding a gate must never produce.
//
// Scanning bodies also catches every adapter in the package rather than only
// the one type the caller passes, so a second adapter added later is covered
// without changing this file.
type protoSurface struct {
	root      string
	protoPath string
	adapter   string
}

func newStateStoreSurface(root string) Surface {
	return &protoSurface{
		root:      root,
		protoPath: "proto/state/v1/state.proto",
		adapter:   "internal/infrastructure/staterpc",
	}
}

func newGatewayChatSurface(root string) Surface {
	return &protoSurface{
		root:      root,
		protoPath: "proto/gateway/v1/chat.proto",
		adapter:   "internal/infrastructure/gatewayrpc",
	}
}

// Name is the proto path itself, so a declaration entry names the exact file
// whose RPCs it is talking about and cannot be confused with another surface.
func (s *protoSurface) Name() string { return s.protoPath }

var (
	rpcPattern     = regexp.MustCompile(`(?m)^\s*rpc\s+([A-Za-z0-9_]+)\s*\(`)
	servicePattern = regexp.MustCompile(`(?m)^\s*service\s+([A-Za-z0-9_]+)\s*\{`)
	// A call through the generated client: `c.client.ListSessions(`. The
	// receiver is whatever the adapter names its field, so match any selector
	// that ends in a generated-client call rather than a fixed receiver name.
	clientCallPattern = regexp.MustCompile(`\bclient\.([A-Z][A-Za-z0-9_]*)\(`)
	// The enclosing method, used to attribute the call in the report.
	methodPattern = regexp.MustCompile(`(?m)^func \([^)]*\)\s+([A-Z][A-Za-z0-9_]*)\s*\(`)
)

func (s *protoSurface) Subjects() ([]Subject, error) {
	raw, err := os.ReadFile(filepath.Join(s.root, s.protoPath))
	if err != nil {
		return nil, fmt.Errorf("read proto: %w", err)
	}
	service := servicePattern.FindSubmatch(raw)
	if service == nil {
		return nil, fmt.Errorf("%s declares no service; the parser is stale", s.protoPath)
	}
	serviceName := string(service[1])

	rpcs := rpcPattern.FindAllSubmatch(raw, -1)
	if len(rpcs) == 0 {
		return nil, fmt.Errorf("%s declares no rpc; refusing to report an empty surface", s.protoPath)
	}

	consumers, err := s.adapterCallSites()
	if err != nil {
		return nil, err
	}

	subjects := make([]Subject, 0, len(rpcs))
	for _, match := range rpcs {
		name := string(match[1])
		subject := Subject{
			Name:     serviceName + "/" + name,
			Evidence: s.protoPath,
		}
		if site, ok := consumers[name]; ok {
			subject.Consumed = true
			subject.Consumer = site
		}
		subjects = append(subjects, subject)
	}
	return subjects, nil
}

// adapterCallSites maps an RPC name to the adapter method that calls it. The
// generated request/response build helpers are counted too, because a stream
// RPC is reached through one.
func (s *protoSurface) adapterCallSites() (map[string]string, error) {
	adapterDir := filepath.Join(s.root, s.adapter)
	entries, err := os.ReadDir(adapterDir)
	if err != nil {
		return nil, fmt.Errorf("read adapter package: %w", err)
	}
	sites := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		// _test.go files prove an adapter works, not that production reaches
		// it. server.go is the serving side, which is the surface being
		// consumed, not a consumer of it.
		if strings.HasSuffix(name, "_test.go") || name == "server.go" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(adapterDir, name))
		if err != nil {
			return nil, fmt.Errorf("read adapter file: %w", err)
		}
		rel := filepath.Join(s.adapter, name)

		// Walk the file linearly so each client call is attributed to the
		// method that encloses it. methods is position-ordered, so the last
		// entry starting before the call is the enclosing one.
		methods := methodPattern.FindAllStringSubmatchIndex(string(body), -1)
		for _, call := range clientCallPattern.FindAllStringSubmatchIndex(string(body), -1) {
			rpcName := string(body[call[2]:call[3]])
			enclosing := ""
			for _, method := range methods {
				if method[0] < call[0] {
					enclosing = string(body[method[2]:method[3]])
					continue
				}
				break
			}
			if _, exists := sites[rpcName]; !exists {
				sites[rpcName] = fmt.Sprintf("%s (%s.%s)", rel, filepath.Base(s.adapter), enclosing)
			}
		}
	}
	return sites, nil
}
